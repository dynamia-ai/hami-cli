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
	"slices"
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
	nvidiaDeviceMajor  = 195
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

type artifactSnapshotter interface {
	Snapshot(*os.File) (*os.File, string, func(), error)
}

type systemArtifactSnapshotter struct {
	filesystemFlags func(string) (uint64, error)
}

// Plugin injects the community HAMi Core into one Docker container.
type Plugin struct {
	goos           string
	goarch         string
	inspector      gpuInspector
	dockerEndpoint dockerEndpointInspector
	snapshotter    artifactSnapshotter
}

// New creates the NVIDIA plugin using the local Linux host for runtime checks.
func New() *Plugin {
	return &Plugin{
		goos:           runtime.GOOS,
		goarch:         runtime.GOARCH,
		inspector:      nvidiaSMIInspector{},
		dockerEndpoint: dockerContextInspector{},
		snapshotter:    systemArtifactSnapshotter{},
	}
}

// Name implements run.Plugin.
func (*Plugin) Name() string { return "nvidia" }

// Inject implements run.Plugin.
func (plugin *Plugin) Inject(ctx context.Context, request hamirun.Request) (hamirun.Injection, error) {
	device, err := validateDevice(request.Device)
	if err != nil {
		return hamirun.Injection{}, err
	}
	memoryBytes, err := parseMemory(request.Memory)
	if err != nil {
		return hamirun.Injection{}, err
	}
	if request.Compute < 1 || request.Compute > 100 {
		return hamirun.Injection{}, fmt.Errorf("--compute must be between 1 and 100, got %d", request.Compute)
	}
	if err := validateDockerOptions(request.DockerArgs[2:]); err != nil {
		return hamirun.Injection{}, err
	}
	dockerArgs := request.DockerArgs
	var optionCleanup func()

	if !request.DryRun {
		if plugin.goos != "linux" || plugin.goarch != "amd64" {
			return hamirun.Injection{}, fmt.Errorf("NVIDIA runtime requires linux/amd64, current platform is %s/%s", plugin.goos, plugin.goarch)
		}
		if err := plugin.dockerEndpoint.ValidateLocal(ctx); err != nil {
			return hamirun.Injection{}, err
		}
		if err := plugin.inspector.Validate(ctx, device); err != nil {
			return hamirun.Injection{}, err
		}
		dockerArgs, optionCleanup, err = pinDockerAssignmentFiles(request.DockerArgs, plugin.snapshotter)
		if err != nil {
			return hamirun.Injection{}, err
		}
		if err := validateDockerOptions(dockerArgs[2:]); err != nil {
			if optionCleanup != nil {
				optionCleanup()
			}
			return hamirun.Injection{}, err
		}
	}

	var snapshotter artifactSnapshotter
	if !request.DryRun {
		snapshotter = plugin.snapshotter
	}
	corePath, coreVersion, coreDigest, cleanup, err := verifyArtifact(request.Core, snapshotter)
	if err != nil {
		if optionCleanup != nil {
			optionCleanup()
		}
		return hamirun.Injection{}, err
	}
	cleanup = combineCleanups(optionCleanup, cleanup)

	coreMountOption := "--mount"
	var coreMountValue string
	if request.DryRun {
		coreMountValue, err = mountValue(
			"type=bind",
			"src="+corePath,
			"dst="+coreContainerPath,
			"readonly",
		)
	} else {
		coreMountOption = "--volume"
		coreMountValue, err = coreVolumeValue(corePath)
	}
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return hamirun.Injection{}, fmt.Errorf("format Core bind mount: %w", err)
	}

	return hamirun.Injection{Args: []string{
		"--gpus", "device=" + device,
		coreMountOption, coreMountValue,
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
	}, DockerArgs: dockerArgs, Cleanup: cleanup}, nil
}

func coreVolumeValue(source string) (string, error) {
	if strings.Contains(source, ":") {
		return "", fmt.Errorf("Core snapshot path %q cannot be represented safely as a Docker volume source", source)
	}
	return source + ":" + coreContainerPath + ":ro,Z", nil
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

func verifyArtifact(artifact hamirun.Artifact, snapshotter artifactSnapshotter) (string, string, string, func(), error) {
	version := strings.TrimSpace(artifact.Version)
	if version == "" || strings.IndexFunc(version, unicode.IsControl) >= 0 {
		return "", "", "", nil, errors.New("--core-version must identify the Core artifact")
	}
	wantDigest := strings.ToLower(strings.TrimSpace(artifact.SHA256))
	wantDigest = strings.TrimPrefix(wantDigest, "sha256:")
	if len(wantDigest) != sha256.Size*2 || strings.IndexFunc(wantDigest, func(r rune) bool {
		return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'))
	}) >= 0 {
		return "", "", "", nil, errors.New("--core-sha256 must be a 64-character SHA-256 digest")
	}

	absPath, err := filepath.Abs(artifact.Path)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("resolve --core path: %w", err)
	}
	source, err := os.Open(absPath)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("open Core artifact %q: %w", absPath, err)
	}
	info, err := source.Stat()
	if err != nil {
		_ = source.Close()
		return "", "", "", nil, fmt.Errorf("stat Core artifact %q: %w", absPath, err)
	}
	if !info.Mode().IsRegular() {
		_ = source.Close()
		return "", "", "", nil, fmt.Errorf("Core artifact %q is not a regular file", absPath)
	}

	candidate := source
	mountPath := absPath
	cleanup := func() { _ = source.Close() }
	keepOpen := false
	defer func() {
		if !keepOpen {
			cleanup()
		}
	}()
	if snapshotter != nil {
		snapshot, snapshotPath, snapshotCleanup, err := snapshotter.Snapshot(source)
		if err != nil {
			return "", "", "", nil, fmt.Errorf("pin Core artifact %q: %w", absPath, err)
		}
		_ = source.Close()
		candidate = snapshot
		mountPath = snapshotPath
		cleanup = snapshotCleanup
		info, err = snapshot.Stat()
		if err != nil {
			return "", "", "", nil, fmt.Errorf("stat pinned Core artifact %q: %w", absPath, err)
		}
	}
	if _, err := candidate.Seek(0, io.SeekStart); err != nil {
		return "", "", "", nil, fmt.Errorf("seek Core artifact %q: %w", absPath, err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, candidate); err != nil {
		return "", "", "", nil, fmt.Errorf("hash Core artifact %q: %w", absPath, err)
	}
	gotDigest := fmt.Sprintf("%x", hash.Sum(nil))
	if gotDigest != wantDigest {
		return "", "", "", nil, fmt.Errorf("Core artifact SHA-256 mismatch: got %s, want %s", gotDigest, wantDigest)
	}
	core, err := elf.NewFile(io.NewSectionReader(candidate, 0, info.Size()))
	if err != nil {
		return "", "", "", nil, fmt.Errorf("Core artifact %q is not an ELF shared library: %w", absPath, err)
	}
	defer core.Close()
	if core.Class != elf.ELFCLASS64 || core.Data != elf.ELFDATA2LSB || core.Machine != elf.EM_X86_64 || core.Type != elf.ET_DYN {
		return "", "", "", nil, fmt.Errorf(
			"Core artifact %q must be a little-endian ELF64 x86-64 shared object, got class=%s data=%s machine=%s type=%s",
			absPath, core.Class, core.Data, core.Machine, core.Type,
		)
	}
	if err := validateLoadableSharedLibrary(core); err != nil {
		return "", "", "", nil, fmt.Errorf("Core artifact %q must be a loadable shared library: %w", absPath, err)
	}
	if snapshotter == nil {
		return mountPath, version, gotDigest, nil, nil
	}
	keepOpen = true
	return mountPath, version, gotDigest, cleanup, nil
}

func validateLoadableSharedLibrary(core *elf.File) error {
	loadSegments := make([]*elf.Prog, 0)
	hasExecutableLoad := false
	var dynamicSegment *elf.Prog
	for _, program := range core.Progs {
		switch program.Type {
		case elf.PT_INTERP:
			return errors.New("ELF contains a program interpreter and is a PIE executable")
		case elf.PT_LOAD:
			if program.Filesz > 0 && program.Memsz >= program.Filesz {
				loadSegments = append(loadSegments, program)
				hasExecutableLoad = hasExecutableLoad || program.Flags&elf.PF_X != 0
			}
		case elf.PT_DYNAMIC:
			if dynamicSegment != nil {
				return errors.New("ELF contains multiple dynamic segments")
			}
			dynamicSegment = program
		}
	}
	if !hasExecutableLoad {
		return errors.New("ELF has no executable load segment")
	}
	if dynamicSegment == nil || dynamicSegment.Filesz < 16 || dynamicSegment.Filesz%16 != 0 {
		return errors.New("ELF has no valid dynamic segment")
	}
	contained := false
	for _, load := range loadSegments {
		if dynamicSegment.Off >= load.Off && dynamicSegment.Off-load.Off <= load.Filesz &&
			dynamicSegment.Filesz <= load.Filesz-(dynamicSegment.Off-load.Off) {
			contained = true
			break
		}
	}
	if !contained {
		return errors.New("ELF dynamic segment is not contained in a load segment")
	}

	reader := dynamicSegment.Open()
	entry := make([]byte, 16)
	for offset := uint64(0); offset < dynamicSegment.Filesz; offset += uint64(len(entry)) {
		if _, err := io.ReadFull(reader, entry); err != nil {
			return fmt.Errorf("read ELF dynamic segment: %w", err)
		}
		tag := elf.DynTag(int64(core.ByteOrder.Uint64(entry[:8])))
		value := core.ByteOrder.Uint64(entry[8:])
		if tag == elf.DT_FLAGS_1 && elf.DynFlag1(value)&elf.DF_1_PIE != 0 {
			return errors.New("ELF dynamic flags identify a static PIE executable")
		}
		if tag == elf.DT_NULL {
			return nil
		}
	}
	return errors.New("ELF dynamic segment has no DT_NULL terminator")
}

type dockerOption struct {
	name       string
	value      string
	argIndex   int
	valueIndex int
}

func pinDockerAssignmentFiles(dockerArgs []string, snapshotter artifactSnapshotter) ([]string, func(), error) {
	options, err := parseDockerOptions(dockerArgs[2:])
	if err != nil {
		return nil, nil, err
	}
	pinnedArgs := slices.Clone(dockerArgs)
	cleanups := make([]func(), 0)
	cleanup := func() {
		for index := len(cleanups) - 1; index >= 0; index-- {
			cleanups[index]()
		}
	}
	for _, option := range options {
		if option.name != "--env-file" && option.name != "--label-file" {
			continue
		}
		source, err := openRegularAssignmentFile(option.value)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("pin Docker option %s=%q: %w", option.name, option.value, err)
		}
		snapshot, snapshotPath, snapshotCleanup, err := snapshotter.Snapshot(source)
		_ = source.Close()
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("pin Docker option %s=%q: %w", option.name, option.value, err)
		}
		_ = snapshot
		cleanups = append(cleanups, snapshotCleanup)
		if option.valueIndex >= 0 {
			pinnedArgs[2+option.valueIndex] = snapshotPath
		} else {
			pinnedArgs[2+option.argIndex] = option.name + "=" + snapshotPath
		}
	}
	if len(cleanups) == 0 {
		return pinnedArgs, nil, nil
	}
	return pinnedArgs, cleanup, nil
}

func combineCleanups(cleanups ...func()) func() {
	filtered := make([]func(), 0, len(cleanups))
	for _, cleanup := range cleanups {
		if cleanup != nil {
			filtered = append(filtered, cleanup)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return func() {
		for index := len(filtered) - 1; index >= 0; index-- {
			filtered[index]()
		}
	}
}

func validateDockerOptions(args []string) error {
	options, err := parseDockerOptions(args)
	if err != nil {
		return err
	}
	remove := false
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
		case "--detach":
			enabled, err := booleanOptionEnabled(option)
			if err != nil {
				return err
			}
			if enabled {
				return errors.New("NVIDIA Core snapshots require a foreground Docker client; --detach is unsupported")
			}
		case "--rm":
			remove, err = booleanOptionEnabled(option)
			if err != nil {
				return err
			}
		case "--restart":
			if option.value != "no" {
				return fmt.Errorf("Docker restart policy %q is unsupported because the Core snapshot is scoped to one foreground run", option.value)
			}
		case "--device":
			if err := validateDockerDevice(option.value); err != nil {
				return err
			}
		case "--device-cgroup-rule":
			if err := validateDeviceCgroupRule(option.value); err != nil {
				return err
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
		case "--volumes-from":
			return errors.New("Docker option --volumes-from cannot be inspected for protected HAMi mount targets")
		}
	}
	if !remove {
		return errors.New("NVIDIA Core snapshots require --rm so the container cannot outlive its verified Core artifact")
	}
	return nil
}

func validateDockerDevice(value string) error {
	parts := strings.SplitN(value, ":", 4)
	if len(parts) == 4 || parts[0] == "" {
		return fmt.Errorf("cannot safely inspect Docker option --device=%q", value)
	}
	if strings.Contains(strings.ToLower(value), "nvidia") {
		return fmt.Errorf("Docker option --device=%q conflicts with HAMi GPU selection", value)
	}
	source := parts[0]
	if !filepath.IsAbs(source) {
		return fmt.Errorf("cannot safely inspect Docker option --device source %q", source)
	}
	isNVIDIADevice, err := inspectDockerDeviceSource(source)
	if err != nil {
		return fmt.Errorf("inspect Docker option --device source %q: %w", source, err)
	}
	if isNVIDIADevice {
		return fmt.Errorf("Docker option --device=%q grants NVIDIA device major %d and conflicts with HAMi GPU selection", value, nvidiaDeviceMajor)
	}
	return nil
}

func validateDeviceCgroupRule(value string) error {
	fields := strings.Fields(value)
	if len(fields) != 3 {
		return fmt.Errorf("cannot safely inspect Docker option --device-cgroup-rule=%q", value)
	}
	deviceType := fields[0]
	major, minor, found := strings.Cut(fields[1], ":")
	if !found || !validDeviceNumber(major) || !validDeviceNumber(minor) || !validDevicePermissions(fields[2]) {
		return fmt.Errorf("cannot safely inspect Docker option --device-cgroup-rule=%q", value)
	}
	if deviceType == "a" || (deviceType == "c" && (major == "*" || deviceNumberEquals(major, nvidiaDeviceMajor))) {
		return fmt.Errorf("Docker option --device-cgroup-rule=%q can grant NVIDIA devices and conflicts with HAMi GPU selection", value)
	}
	if deviceType != "b" && deviceType != "c" {
		return fmt.Errorf("cannot safely inspect Docker option --device-cgroup-rule=%q", value)
	}
	return nil
}

func validDeviceNumber(value string) bool {
	if value == "*" {
		return true
	}
	_, err := strconv.ParseUint(value, 10, 32)
	return err == nil
}

func deviceNumberEquals(value string, want uint32) bool {
	number, err := strconv.ParseUint(value, 10, 32)
	return err == nil && uint32(number) == want
}

func validDevicePermissions(value string) bool {
	if value == "" {
		return false
	}
	seen := make(map[rune]struct{}, len(value))
	for _, permission := range value {
		if !strings.ContainsRune("rwm", permission) {
			return false
		}
		if _, duplicate := seen[permission]; duplicate {
			return false
		}
		seen[permission] = struct{}{}
	}
	return true
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
	file, err := openRegularAssignmentFile(filename)
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
		if lookup == nil {
			assignments = append(assignments, key)
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
	target := ""
	for _, field := range fields {
		key, fieldValue, found := strings.Cut(field, "=")
		if found && (key == "dst" || key == "destination" || key == "target") {
			target = fieldValue
		}
	}
	if target == "" {
		return "", fmt.Errorf("Docker --mount value %q has no destination", value)
	}
	return target, nil
}

func volumeTarget(value string) string {
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		return parts[0]
	}
	// Docker permits [source:]target[:options]. With two fields, an absolute
	// first field followed by a non-absolute field is the target:options form.
	if len(parts) == 2 && path.IsAbs(parts[0]) && !path.IsAbs(parts[1]) {
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
	"--mount": {}, "--name": {}, "--net": {}, "--network": {}, "--network-alias": {}, "--oom-score-adj": {},
	"--pid": {}, "--pids-limit": {}, "--platform": {}, "--publish": {}, "--pull": {},
	"--restart": {}, "--runtime": {}, "--security-opt": {}, "--shm-size": {},
	"--stop-signal": {}, "--stop-timeout": {}, "--storage-opt": {}, "--sysctl": {},
	"--tmpfs": {}, "--ulimit": {}, "--user": {}, "--userns": {}, "--uts": {},
	"--volume": {}, "--volume-driver": {}, "--volumes-from": {}, "--workdir": {},
}

var longBooleanOptions = map[string]struct{}{
	"--detach": {}, "--disable-content-trust": {}, "--help": {}, "--init": {}, "--interactive": {}, "--no-healthcheck": {},
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
				option := dockerOption{name: name, value: value, argIndex: index, valueIndex: -1}
				if !hasValue {
					index++
					if index >= len(args) {
						return nil, fmt.Errorf("Docker option %s requires a value", name)
					}
					value = args[index]
					option.valueIndex = index
				}
				option.value = value
				options = append(options, option)
				index++
				continue
			}
			if _, found := longBooleanOptions[name]; found {
				options = append(options, dockerOption{name: name, value: value, argIndex: index, valueIndex: -1})
				index++
				continue
			}
			if hasValue {
				options = append(options, dockerOption{name: name, value: value, argIndex: index, valueIndex: -1})
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
	if contextName := strings.TrimSpace(os.Getenv("DOCKER_CONTEXT")); contextName != "" {
		return validateDockerContextEndpoint(ctx, contextName)
	}
	if endpoint := strings.TrimSpace(os.Getenv("DOCKER_HOST")); endpoint != "" {
		return validateLocalDockerEndpoint(endpoint)
	}
	return validateDockerContextEndpoint(ctx, "")
}

func validateDockerContextEndpoint(ctx context.Context, contextName string) error {
	args := []string{"context", "inspect", "--format", `{{(index .Endpoints "docker").Host}}`}
	if contextName != "" {
		args = append(args, contextName)
	}
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("inspect Docker endpoint: %w", err)
	}
	return validateLocalDockerEndpoint(strings.TrimSpace(string(output)))
}

func validateLocalDockerEndpoint(endpoint string) error {
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
