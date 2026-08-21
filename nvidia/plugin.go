/*
Copyright 2026 The HAMi Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package nvidia implements the Linux NVIDIA HAMi Core injection contract.
package nvidia

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	hamirun "github.com/dynamia-ai/hami-cli/run"
)

const (
	coreContainerPath  = "/usr/local/vgpu/libvgpu.so"
	stateContainerPath = "/tmp/hami-lite"
	lockContainerPath  = "/tmp/vgpulock"
	cacheContainerPath = stateContainerPath + "/cudevshr.cache"
)

var (
	protectedEnvironment = map[string]struct{}{
		"CUDA_DEVICE_MEMORY_LIMIT":        {},
		"CUDA_DEVICE_MEMORY_SHARED_CACHE": {},
		"CUDA_DEVICE_SM_LIMIT":            {},
		"CUDA_REDIRECT":                   {},
		"CUDA_VISIBLE_DEVICES":            {},
		"GPU_CORE_UTILIZATION_POLICY":     {},
		"LD_PRELOAD":                      {},
		"NVIDIA_VISIBLE_DEVICES":          {},
	}
	protectedMountTargets = []string{coreContainerPath, stateContainerPath, lockContainerPath}
)

type gpuInspector interface {
	Validate(context.Context, string) error
}

type dockerEndpointInspector interface {
	ValidateLocal(context.Context) error
}

// Plugin injects the community HAMi Core into one Docker container.
type Plugin struct {
	goos           string
	goarch         string
	inspector      gpuInspector
	dockerEndpoint dockerEndpointInspector
}

// New creates the NVIDIA plugin using the local Linux host for runtime checks.
func New() *Plugin {
	return &Plugin{
		goos:           runtime.GOOS,
		goarch:         runtime.GOARCH,
		inspector:      nvidiaSMIInspector{},
		dockerEndpoint: dockerContextInspector{},
	}
}

// Name implements run.Plugin.
func (*Plugin) Name() string { return "nvidia" }

// Inject implements run.Plugin.
func (plugin *Plugin) Inject(ctx context.Context, request hamirun.Request) ([]string, error) {
	device, err := validateDevice(request.Device)
	if err != nil {
		return nil, err
	}
	memoryBytes, err := parseMemory(request.Memory)
	if err != nil {
		return nil, err
	}
	if request.Compute < 1 || request.Compute > 100 {
		return nil, fmt.Errorf("--compute must be between 1 and 100, got %d", request.Compute)
	}
	corePath, coreVersion, coreDigest, err := verifyArtifact(request.Core)
	if err != nil {
		return nil, err
	}
	if err := validateDockerOptions(request.DockerArgs[2:]); err != nil {
		return nil, err
	}

	if !request.DryRun {
		if plugin.goos != "linux" || plugin.goarch != "amd64" {
			return nil, fmt.Errorf("NVIDIA runtime requires linux/amd64, current platform is %s/%s", plugin.goos, plugin.goarch)
		}
		if err := plugin.dockerEndpoint.ValidateLocal(ctx); err != nil {
			return nil, err
		}
		if err := plugin.inspector.Validate(ctx, device); err != nil {
			return nil, err
		}
	}

	bindMount, err := mountValue(
		"type=bind",
		"src="+corePath,
		"dst="+coreContainerPath,
		"readonly",
	)
	if err != nil {
		return nil, fmt.Errorf("format Core bind mount: %w", err)
	}

	return []string{
		"--gpus", "device=" + device,
		"--mount", bindMount,
		"--mount", "type=tmpfs,dst=" + stateContainerPath + ",tmpfs-mode=1777",
		"--mount", "type=tmpfs,dst=" + lockContainerPath + ",tmpfs-mode=1777",
		"--env", "LD_PRELOAD=" + coreContainerPath,
		"--env", "CUDA_REDIRECT=" + coreContainerPath,
		"--env", "CUDA_DEVICE_MEMORY_LIMIT=" + strconv.FormatUint(memoryBytes, 10),
		"--env", "CUDA_DEVICE_SM_LIMIT=" + strconv.Itoa(request.Compute),
		"--env", "CUDA_DEVICE_MEMORY_SHARED_CACHE=" + cacheContainerPath,
		"--env", "NVIDIA_DRIVER_CAPABILITIES=compute,utility",
		"--label", "io.hami.lite.managed=true",
		"--label", "io.hami.lite.vendor=nvidia",
		"--label", "io.hami.lite.core.version=" + coreVersion,
		"--label", "io.hami.lite.core.sha256=" + coreDigest,
	}, nil
}

func validateDevice(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("--device is required")
	}
	if strings.Contains(value, ",") || strings.HasPrefix(value, "MIG-") {
		return "", fmt.Errorf("--device accepts exactly one NVIDIA GPU index or GPU UUID, got %q", value)
	}
	if _, err := strconv.ParseUint(value, 10, 31); err == nil {
		return value, nil
	}
	if len(value) > len("GPU-") && strings.HasPrefix(value, "GPU-") && strings.IndexFunc(value[4:], func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-')
	}) == -1 {
		return value, nil
	}
	return "", fmt.Errorf("--device must be one GPU index or GPU UUID, got %q", value)
}

func parseMemory(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("--memory is required")
	}

	multipliers := []struct {
		suffix     string
		multiplier uint64
	}{
		{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10},
		{"G", 1 << 30}, {"g", 1 << 30}, {"M", 1 << 20}, {"m", 1 << 20},
		{"K", 1 << 10}, {"k", 1 << 10}, {"B", 1},
	}
	multiplier := uint64(1)
	digits := value
	for _, unit := range multipliers {
		if strings.HasSuffix(value, unit.suffix) {
			multiplier = unit.multiplier
			digits = strings.TrimSuffix(value, unit.suffix)
			break
		}
	}
	quantity, err := strconv.ParseUint(digits, 10, 64)
	if err != nil || quantity == 0 || quantity > math.MaxUint64/multiplier {
		return 0, fmt.Errorf("--memory must be a positive integer of bytes or use KiB, MiB, or GiB, got %q", value)
	}
	return quantity * multiplier, nil
}

func verifyArtifact(artifact hamirun.Artifact) (string, string, string, error) {
	version := strings.TrimSpace(artifact.Version)
	if version == "" || strings.IndexFunc(version, unicode.IsControl) >= 0 {
		return "", "", "", errors.New("--core-version must identify the Core artifact")
	}
	wantDigest := strings.ToLower(strings.TrimSpace(artifact.SHA256))
	wantDigest = strings.TrimPrefix(wantDigest, "sha256:")
	if len(wantDigest) != sha256.Size*2 || strings.IndexFunc(wantDigest, func(r rune) bool {
		return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'))
	}) >= 0 {
		return "", "", "", errors.New("--core-sha256 must be a 64-character SHA-256 digest")
	}

	absPath, err := filepath.Abs(artifact.Path)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve --core path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", "", "", fmt.Errorf("stat Core artifact %q: %w", absPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", "", fmt.Errorf("Core artifact %q is not a regular file", absPath)
	}
	file, err := os.Open(absPath)
	if err != nil {
		return "", "", "", fmt.Errorf("open Core artifact %q: %w", absPath, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", "", "", fmt.Errorf("hash Core artifact %q: %w", absPath, err)
	}
	gotDigest := fmt.Sprintf("%x", hash.Sum(nil))
	if gotDigest != wantDigest {
		return "", "", "", fmt.Errorf("Core artifact SHA-256 mismatch: got %s, want %s", gotDigest, wantDigest)
	}
	core, err := elf.Open(absPath)
	if err != nil {
		return "", "", "", fmt.Errorf("Core artifact %q is not an ELF shared library: %w", absPath, err)
	}
	defer core.Close()
	if core.Class != elf.ELFCLASS64 || core.Data != elf.ELFDATA2LSB || core.Machine != elf.EM_X86_64 || core.Type != elf.ET_DYN {
		return "", "", "", fmt.Errorf(
			"Core artifact %q must be a little-endian ELF64 x86-64 shared object, got class=%s data=%s machine=%s type=%s",
			absPath, core.Class, core.Data, core.Machine, core.Type,
		)
	}
	return absPath, version, gotDigest, nil
}

type dockerOption struct {
	name  string
	value string
}

func validateDockerOptions(args []string) error {
	options, err := parseDockerOptions(args)
	if err != nil {
		return err
	}
	for _, option := range options {
		switch option.name {
		case "--gpus":
			return errors.New("Docker option --gpus conflicts with HAMi GPU selection")
		case "--runtime":
			if option.value != "nvidia" {
				return fmt.Errorf("Docker runtime %q conflicts with the NVIDIA runtime contract", option.value)
			}
		case "--privileged":
			enabled, err := booleanOptionEnabled(option)
			if err != nil {
				return err
			}
			if enabled {
				return errors.New("Docker option --privileged conflicts with single-GPU device isolation")
			}
		case "--device":
			if strings.Contains(strings.ToLower(option.value), "nvidia") {
				return fmt.Errorf("Docker option --device=%q conflicts with HAMi GPU selection", option.value)
			}
		case "--env":
			if err := validateEnvironmentAssignment(option.value); err != nil {
				return err
			}
		case "--env-file":
			if err := validateAssignmentFile(option.value, os.LookupEnv, validateEnvironmentAssignment, "environment"); err != nil {
				return err
			}
		case "--label":
			if err := validateLabelAssignment(option.value); err != nil {
				return err
			}
		case "--label-file":
			if err := validateAssignmentFile(option.value, nil, validateLabelAssignment, "label"); err != nil {
				return err
			}
		case "--mount":
			target, err := mountTarget(option.value)
			if err != nil {
				return err
			}
			if protectedTarget(target) {
				return fmt.Errorf("Docker mount target %q conflicts with HAMi Core injection", target)
			}
		case "--volume":
			target := volumeTarget(option.value)
			if protectedTarget(target) {
				return fmt.Errorf("Docker volume target %q conflicts with HAMi Core injection", target)
			}
		case "--tmpfs":
			target, _, _ := strings.Cut(option.value, ":")
			if protectedTarget(target) {
				return fmt.Errorf("Docker tmpfs target %q conflicts with HAMi Core injection", target)
			}
		}
	}
	return nil
}

func booleanOptionEnabled(option dockerOption) (bool, error) {
	if option.value == "" {
		return true, nil
	}
	enabled, err := strconv.ParseBool(option.value)
	if err != nil {
		return false, fmt.Errorf("Docker option %s has invalid boolean value %q", option.name, option.value)
	}
	return enabled, nil
}

func protectedEnvironmentKey(key string) bool {
	if _, found := protectedEnvironment[key]; found {
		return true
	}
	return strings.HasPrefix(key, "CUDA_DEVICE_MEMORY_LIMIT_") || strings.HasPrefix(key, "CUDA_DEVICE_SM_LIMIT_")
}

func validateEnvironmentAssignment(assignment string) error {
	key, value, hasValue := strings.Cut(strings.TrimSpace(assignment), "=")
	if key == "NVIDIA_DRIVER_CAPABILITIES" {
		if !hasValue {
			var found bool
			value, found = os.LookupEnv(key)
			if !found {
				return nil
			}
		}
		if !hasRequiredDriverCapabilities(value) {
			return fmt.Errorf("Docker environment %s must include compute and utility, got %q", key, value)
		}
		return nil
	}
	if protectedEnvironmentKey(key) {
		return fmt.Errorf("Docker environment %s conflicts with HAMi Core injection", key)
	}
	return nil
}

func hasRequiredDriverCapabilities(value string) bool {
	if value == "all" {
		return true
	}
	capabilities := make(map[string]struct{})
	for capability := range strings.SplitSeq(value, ",") {
		capabilities[strings.TrimSpace(capability)] = struct{}{}
	}
	_, compute := capabilities["compute"]
	_, utility := capabilities["utility"]
	return compute && utility
}

func validateLabelAssignment(assignment string) error {
	key := assignmentKey(assignment)
	if strings.HasPrefix(key, "io.hami.lite.") {
		return fmt.Errorf("Docker label %s is managed by hami-cli", key)
	}
	return nil
}

func assignmentKey(value string) string {
	key, _, _ := strings.Cut(strings.TrimSpace(value), "=")
	return key
}

func validateAssignmentFile(filename string, lookup func(string) (string, bool), validate func(string) error, kind string) error {
	assignments, err := parseAssignmentFile(filename, lookup)
	if err != nil {
		return fmt.Errorf("parse Docker %s file %q: %w", kind, filename, err)
	}
	for _, assignment := range assignments {
		if err := validate(assignment); err != nil {
			return fmt.Errorf("Docker %s file %q: %w", kind, filename, err)
		}
	}
	return nil
}

// parseAssignmentFile mirrors Docker CLI's line-delimited kvfile contract so
// conflict detection sees the same keys Docker will pass to the daemon.
func parseAssignmentFile(filename string, lookup func(string) (string, bool)) ([]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	assignments := make([]string, 0)
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		scanned := scanner.Bytes()
		if !utf8.Valid(scanned) {
			return nil, fmt.Errorf("invalid UTF-8 at line %d", lineNumber)
		}
		if lineNumber == 1 {
			scanned = bytes.TrimPrefix(scanned, []byte{0xEF, 0xBB, 0xBF})
		}
		line := strings.TrimLeftFunc(string(scanned), unicode.IsSpace)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, hasValue := strings.Cut(line, "=")
		if key == "" {
			return nil, fmt.Errorf("no key at line %d", lineNumber)
		}
		if strings.ContainsAny(key, " \t\x00") {
			return nil, fmt.Errorf("key %q contains whitespace or NUL at line %d", key, lineNumber)
		}
		if hasValue {
			assignments = append(assignments, key+"="+value)
			continue
		}
		if lookup != nil {
			if inherited, found := lookup(key); found {
				assignments = append(assignments, key+"="+inherited)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return assignments, nil
}

func mountTarget(value string) (string, error) {
	reader := csv.NewReader(strings.NewReader(value))
	fields, err := reader.Read()
	if err != nil {
		return "", fmt.Errorf("parse Docker --mount value %q: %w", value, err)
	}
	for _, field := range fields {
		key, fieldValue, found := strings.Cut(field, "=")
		if found && (key == "dst" || key == "destination" || key == "target") {
			return fieldValue, nil
		}
	}
	return "", fmt.Errorf("Docker --mount value %q has no destination", value)
}

func volumeTarget(value string) string {
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		return parts[0]
	}
	return parts[1]
}

func protectedTarget(target string) bool {
	target = path.Clean(target)
	if target == "/dev" || strings.HasPrefix(target, "/dev/nvidia") {
		return true
	}
	for _, protected := range protectedMountTargets {
		if target == protected || strings.HasPrefix(target, protected+"/") || strings.HasPrefix(protected, target+"/") {
			return true
		}
	}
	return false
}

func mountValue(fields ...string) (string, error) {
	var output strings.Builder
	writer := csv.NewWriter(&output)
	if err := writer.Write(fields); err != nil {
		return "", err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	return strings.TrimSuffix(output.String(), "\n"), nil
}

var longOptionsWithValues = map[string]struct{}{
	"--add-host": {}, "--annotation": {}, "--attach": {}, "--blkio-weight": {},
	"--blkio-weight-device": {}, "--cap-add": {}, "--cap-drop": {}, "--cgroup-parent": {},
	"--cgroupns": {}, "--cidfile": {}, "--cpu-count": {}, "--cpu-percent": {},
	"--cpu-period": {}, "--cpu-quota": {}, "--cpu-rt-period": {}, "--cpu-rt-runtime": {},
	"--cpu-shares": {}, "--cpus": {}, "--cpuset-cpus": {}, "--cpuset-mems": {},
	"--detach-keys": {}, "--device": {}, "--device-cgroup-rule": {}, "--device-read-bps": {},
	"--device-read-iops": {}, "--device-write-bps": {}, "--device-write-iops": {},
	"--dns": {}, "--dns-option": {}, "--dns-search": {}, "--domainname": {},
	"--entrypoint": {}, "--env": {}, "--env-file": {}, "--expose": {}, "--gpus": {},
	"--group-add": {}, "--health-cmd": {}, "--health-interval": {}, "--health-retries": {},
	"--health-start-interval": {}, "--health-start-period": {}, "--health-timeout": {},
	"--hostname": {}, "--io-maxbandwidth": {}, "--io-maxiops": {}, "--ip": {}, "--ip6": {},
	"--ipc": {}, "--isolation": {}, "--label": {}, "--label-file": {}, "--link": {},
	"--link-local-ip": {}, "--log-driver": {}, "--log-opt": {}, "--mac-address": {},
	"--memory": {}, "--memory-reservation": {}, "--memory-swap": {}, "--memory-swappiness": {},
	"--mount": {}, "--name": {}, "--network": {}, "--network-alias": {}, "--oom-score-adj": {},
	"--pid": {}, "--pids-limit": {}, "--platform": {}, "--publish": {}, "--pull": {},
	"--restart": {}, "--runtime": {}, "--security-opt": {}, "--shm-size": {},
	"--stop-signal": {}, "--stop-timeout": {}, "--storage-opt": {}, "--sysctl": {},
	"--tmpfs": {}, "--ulimit": {}, "--user": {}, "--userns": {}, "--uts": {},
	"--volume": {}, "--volume-driver": {}, "--volumes-from": {}, "--workdir": {},
}

var longBooleanOptions = map[string]struct{}{
	"--detach": {}, "--help": {}, "--init": {}, "--interactive": {}, "--no-healthcheck": {},
	"--oom-kill-disable": {}, "--privileged": {}, "--publish-all": {}, "--quiet": {},
	"--read-only": {}, "--rm": {}, "--sig-proxy": {}, "--tty": {}, "--use-api-socket": {},
}

var shortOptionsWithValues = map[byte]string{
	'a': "--attach", 'c': "--cpu-shares", 'e': "--env", 'h': "--hostname",
	'l': "--label", 'm': "--memory", 'p': "--publish", 'u': "--user",
	'v': "--volume", 'w': "--workdir",
}

var shortBooleanOptions = map[byte]string{
	'd': "--detach", 'i': "--interactive", 'P': "--publish-all", 'q': "--quiet", 't': "--tty",
}

func parseDockerOptions(args []string) ([]dockerOption, error) {
	options := make([]dockerOption, 0)
	for index := 0; index < len(args); {
		token := args[index]
		if token == "--" {
			index++
			if index >= len(args) {
				return nil, errors.New("docker run requires an IMAGE after --")
			}
			return options, nil
		}
		if token == "-" || !strings.HasPrefix(token, "-") {
			return options, nil
		}
		if strings.HasPrefix(token, "--") {
			name, value, hasValue := strings.Cut(token, "=")
			if _, found := longOptionsWithValues[name]; found {
				if !hasValue {
					index++
					if index >= len(args) {
						return nil, fmt.Errorf("Docker option %s requires a value", name)
					}
					value = args[index]
				}
				options = append(options, dockerOption{name: name, value: value})
				index++
				continue
			}
			if _, found := longBooleanOptions[name]; found {
				options = append(options, dockerOption{name: name, value: value})
				index++
				continue
			}
			if hasValue {
				options = append(options, dockerOption{name: name, value: value})
				index++
				continue
			}
			return nil, fmt.Errorf("cannot safely inspect unknown Docker option %q", name)
		}

		if flags, value, found := strings.Cut(token[1:], "="); found && allShortBooleanOptions(flags) {
			for shortIndex := range len(flags) {
				option := dockerOption{name: shortBooleanOptions[flags[shortIndex]]}
				if shortIndex == len(flags)-1 {
					option.value = value
				}
				options = append(options, option)
			}
			index++
			continue
		}

		consumedNext := false
		for shortIndex := 1; shortIndex < len(token); shortIndex++ {
			shortName := token[shortIndex]
			if name, found := shortBooleanOptions[shortName]; found {
				options = append(options, dockerOption{name: name})
				continue
			}
			name, found := shortOptionsWithValues[shortName]
			if !found {
				return nil, fmt.Errorf("cannot safely inspect unknown Docker option -%c", shortName)
			}
			value := strings.TrimPrefix(token[shortIndex+1:], "=")
			if value == "" {
				index++
				if index >= len(args) {
					return nil, fmt.Errorf("Docker option -%c requires a value", shortName)
				}
				value = args[index]
				consumedNext = true
			}
			options = append(options, dockerOption{name: name, value: value})
			break
		}
		index++
		if consumedNext {
			continue
		}
	}
	return nil, errors.New("docker run requires an IMAGE")
}

func allShortBooleanOptions(flags string) bool {
	if flags == "" {
		return false
	}
	for index := range len(flags) {
		if _, found := shortBooleanOptions[flags[index]]; !found {
			return false
		}
	}
	return true
}

type nvidiaSMIInspector struct{}

type dockerContextInspector struct{}

func (dockerContextInspector) ValidateLocal(ctx context.Context) error {
	command := exec.CommandContext(ctx, "docker", "context", "inspect", "--format", `{{(index .Endpoints "docker").Host}}`)
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("inspect Docker endpoint: %w", err)
	}
	endpoint := strings.TrimSpace(string(output))
	if !strings.HasPrefix(endpoint, "unix://") {
		return fmt.Errorf("NVIDIA local runtime requires a local unix Docker endpoint, current endpoint is %q", endpoint)
	}
	return nil
}

func (nvidiaSMIInspector) Validate(ctx context.Context, requested string) error {
	command := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=index,uuid", "--format=csv,noheader")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("inspect NVIDIA GPUs with nvidia-smi: %w", err)
	}
	for line := range strings.Lines(string(output)) {
		index, uuid, found := strings.Cut(strings.TrimSpace(line), ",")
		if !found {
			continue
		}
		if requested == strings.TrimSpace(index) || requested == strings.TrimSpace(uuid) {
			return nil
		}
	}
	return fmt.Errorf("NVIDIA GPU %q was not reported by nvidia-smi", requested)
}
