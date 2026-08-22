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

package nvidia

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	hamirun "github.com/dynamia-ai/hami-cli/run"
)

type fakeInspector struct {
	wantDevice string
	err        error
	called     bool
}

type fakeDockerEndpoint struct {
	err    error
	called bool
}

type copyingSnapshotter struct {
	directory string
}

func (snapshotter copyingSnapshotter) Snapshot(source *os.File) (*os.File, string, func(), error) {
	if _, err := source.Seek(0, 0); err != nil {
		return nil, "", nil, err
	}
	snapshot, err := os.CreateTemp(snapshotter.directory, "hami-core-*")
	if err != nil {
		return nil, "", nil, err
	}
	if _, err := snapshot.ReadFrom(source); err != nil {
		_ = snapshot.Close()
		return nil, "", nil, err
	}
	if _, err := snapshot.Seek(0, 0); err != nil {
		_ = snapshot.Close()
		return nil, "", nil, err
	}
	return snapshot, snapshot.Name(), func() { _ = snapshot.Close() }, nil
}

func (endpoint *fakeDockerEndpoint) ValidateLocal(context.Context) error {
	endpoint.called = true
	return endpoint.err
}

func (inspector *fakeInspector) Validate(_ context.Context, device string) error {
	inspector.called = true
	if device != inspector.wantDevice {
		return fmt.Errorf("device = %q, want %q", device, inspector.wantDevice)
	}
	return inspector.err
}

func testArtifact(t *testing.T) hamirun.Artifact {
	t.Helper()
	content := testELFSharedObject(t)
	path := filepath.Join(t.TempDir(), "libvgpu.so")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write Core artifact: %v", err)
	}
	digest := sha256.Sum256(content)
	return hamirun.Artifact{Path: path, Version: "v2.7.0-test", SHA256: fmt.Sprintf("%x", digest)}
}

func testELFSharedObject(t *testing.T) []byte {
	t.Helper()
	return testELFImage(t, false, 0)
}

func testELFPIE(t *testing.T) []byte {
	t.Helper()
	return testELFImage(t, true, 0)
}

func testELFStaticPIE(t *testing.T) []byte {
	t.Helper()
	return testELFImage(t, false, uint64(elf.DF_1_PIE))
}

func testELFHeaderOnly(t *testing.T) []byte {
	t.Helper()
	header := make([]byte, 64)
	copy(header, []byte{0x7f, 'E', 'L', 'F', byte(elfClass64), byte(elfDataLittleEndian), 1, 0})
	binary.LittleEndian.PutUint16(header[16:], 3)  // ET_DYN
	binary.LittleEndian.PutUint16(header[18:], 62) // EM_X86_64
	binary.LittleEndian.PutUint32(header[20:], 1)  // EV_CURRENT
	binary.LittleEndian.PutUint16(header[52:], 64) // ELF header size
	binary.LittleEndian.PutUint16(header[54:], 56) // program header entry size
	binary.LittleEndian.PutUint16(header[58:], 64) // section header entry size
	return bytes.Clone(header)
}

func testELFImage(t *testing.T, interpreter bool, flagsOne uint64) []byte {
	t.Helper()
	const (
		headerSize        = 64
		programHeaderSize = 56
		dynamicEntrySize  = 16
	)
	programCount := 2
	if interpreter {
		programCount++
	}
	dynamicEntryCount := 1
	if flagsOne != 0 {
		dynamicEntryCount++
	}
	dynamicOffset := headerSize + programCount*programHeaderSize
	dynamicSize := dynamicEntryCount * dynamicEntrySize
	content := make([]byte, dynamicOffset+dynamicSize)
	copy(content, []byte{0x7f, 'E', 'L', 'F', byte(elfClass64), byte(elfDataLittleEndian), 1, 0})
	binary.LittleEndian.PutUint16(content[16:], 3)  // ET_DYN
	binary.LittleEndian.PutUint16(content[18:], 62) // EM_X86_64
	binary.LittleEndian.PutUint32(content[20:], 1)  // EV_CURRENT
	binary.LittleEndian.PutUint64(content[32:], headerSize)
	binary.LittleEndian.PutUint16(content[52:], headerSize)
	binary.LittleEndian.PutUint16(content[54:], programHeaderSize)
	binary.LittleEndian.PutUint16(content[56:], uint16(programCount))
	binary.LittleEndian.PutUint16(content[58:], 64)

	writeProgramHeader := func(index int, programType, flags uint32, offset, size, alignment uint64) {
		base := headerSize + index*programHeaderSize
		binary.LittleEndian.PutUint32(content[base:], programType)
		binary.LittleEndian.PutUint32(content[base+4:], flags)
		binary.LittleEndian.PutUint64(content[base+8:], offset)
		binary.LittleEndian.PutUint64(content[base+16:], offset)
		binary.LittleEndian.PutUint64(content[base+24:], offset)
		binary.LittleEndian.PutUint64(content[base+32:], size)
		binary.LittleEndian.PutUint64(content[base+40:], size)
		binary.LittleEndian.PutUint64(content[base+48:], alignment)
	}
	writeProgramHeader(0, 1, 5, 0, uint64(len(content)), 0x1000)               // PT_LOAD, PF_R|PF_X
	writeProgramHeader(1, 2, 6, uint64(dynamicOffset), uint64(dynamicSize), 8) // PT_DYNAMIC, PF_R|PF_W
	if interpreter {
		writeProgramHeader(2, 3, 4, uint64(dynamicOffset), 1, 1) // PT_INTERP, PF_R
	}
	if flagsOne != 0 {
		binary.LittleEndian.PutUint64(content[dynamicOffset:], uint64(elf.DT_FLAGS_1))
		binary.LittleEndian.PutUint64(content[dynamicOffset+8:], flagsOne)
	}
	return content
}

const (
	elfClass64          = 2
	elfDataLittleEndian = 1
)

func testRequest(t *testing.T, dockerArgs []string) hamirun.Request {
	t.Helper()
	hasRemove := false
	for _, argument := range dockerArgs[2:] {
		if argument == "--rm" || strings.HasPrefix(argument, "--rm=") {
			hasRemove = true
			break
		}
	}
	if !hasRemove {
		withRemove := make([]string, 0, len(dockerArgs)+1)
		withRemove = append(withRemove, dockerArgs[:2]...)
		withRemove = append(withRemove, "--rm")
		dockerArgs = append(withRemove, dockerArgs[2:]...)
	}
	return hamirun.Request{
		DockerArgs: dockerArgs,
		Device:     "0",
		Memory:     "8GiB",
		Compute:    50,
		Core:       testArtifact(t),
		DryRun:     true,
	}
}

func TestInjectBuildsNVIDIAContract(t *testing.T) {
	t.Parallel()
	request := testRequest(t, []string{
		"docker", "run", "--rm", "-it", "--name", "training",
		"-v", "/tmp/work space:/workspace", "--network", "host",
		"pytorch/pytorch:latest", "python", "train.py", "--epochs", "10",
	})
	injection, err := New().Inject(t.Context(), request)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	joined := strings.Join(injection.Args, "\n")
	for _, want := range []string{
		"--gpus\ndevice=0",
		"--security-opt\nno-new-privileges=true",
		"--volume\n" + request.Core.Path + ":/usr/local/vgpu/libvgpu.so:ro,z",
		"type=tmpfs,dst=/tmp/hami-lite,tmpfs-mode=1777",
		"type=tmpfs,dst=/tmp/vgpulock,tmpfs-mode=1777",
		"LD_PRELOAD=/usr/local/vgpu/libvgpu.so",
		"CUDA_REDIRECT=/usr/local/vgpu/libvgpu.so",
		"CUDA_DISABLE_CONTROL=false",
		"CUDA_DEVICE_MEMORY_LIMIT=8589934592",
		"CUDA_DEVICE_SM_LIMIT=50",
		"CUDA_DEVICE_MEMORY_SHARED_CACHE=/tmp/hami-lite/cudevshr.cache",
		"GPU_CORE_UTILIZATION_POLICY=default",
		"NVIDIA_DRIVER_CAPABILITIES=compute,utility",
		"io.hami.lite.core.version=v2.7.0-test",
		"io.hami.lite.core.sha256=" + request.Core.SHA256,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("injection missing %q:\n%s", want, joined)
		}
	}
	if slices.Contains(injection.Args, request.DockerArgs[2]) {
		t.Fatalf("plugin injection contains user Docker option: %#v", injection)
	}
}

func TestInjectRejectsDockerConflicts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "gpus", args: []string{"--gpus", "all", "image"}, want: "--gpus"},
		{name: "runtime", args: []string{"--runtime=runc", "image"}, want: "runtime"},
		{name: "privileged", args: []string{"--privileged", "image"}, want: "--privileged"},
		{name: "disable no new privileges", args: []string{"--security-opt", "no-new-privileges=false", "image"}, want: "no-new-privileges=true"},
		{name: "disable no new privileges legacy syntax", args: []string{"--security-opt=no-new-privileges:false", "image"}, want: "no-new-privileges=true"},
		{name: "non-NVIDIA device", args: []string{"--device", os.DevNull, "image"}, want: "cannot be pinned"},
		{name: "NVIDIA device", args: []string{"--device", "/dev/nvidia0", "image"}, want: "--device"},
		{name: "NVIDIA device alias", args: []string{"--device", "/dev/char/195:1:/dev/altgpu", "image"}, want: "--device"},
		{name: "NVIDIA device cgroup rule", args: []string{"--device-cgroup-rule", "c 195:* rmw", "image"}, want: "--device-cgroup-rule"},
		{name: "inherited volumes", args: []string{"--volumes-from", "other", "image"}, want: "--volumes-from"},
		{name: "environment", args: []string{"--env", "LD_PRELOAD=/bad.so", "image"}, want: "LD_PRELOAD"},
		{name: "disable control environment", args: []string{"--env", "CUDA_DISABLE_CONTROL=true", "image"}, want: "CUDA_DISABLE_CONTROL"},
		{name: "disable core policy environment", args: []string{"--env", "GPU_CORE_UTILIZATION_POLICY=disable", "image"}, want: "GPU_CORE_UTILIZATION_POLICY"},
		{name: "short environment", args: []string{"-eCUDA_DEVICE_SM_LIMIT=90", "image"}, want: "CUDA_DEVICE_SM_LIMIT"},
		{name: "per-device environment", args: []string{"-e", "CUDA_DEVICE_MEMORY_LIMIT_0=1g", "image"}, want: "CUDA_DEVICE_MEMORY_LIMIT_0"},
		{name: "driver capabilities", args: []string{"-e", "NVIDIA_DRIVER_CAPABILITIES=video,utility", "image"}, want: "compute and utility"},
		{name: "mount", args: []string{"--mount", "type=bind,src=/tmp,dst=/usr/local/vgpu", "image"}, want: "mount target"},
		{name: "mount destination alias override", args: []string{"--mount", "type=bind,src=/tmp,dst=/workspace,target=/tmp/hami-lite/cudevshr.cache", "image"}, want: "mount target"},
		{name: "NVIDIA device mount", args: []string{"--mount", "type=bind,src=/dev,dst=/dev", "image"}, want: "mount target"},
		{name: "volume", args: []string{"-v", "/tmp:/tmp/vgpulock", "image"}, want: "volume target"},
		{name: "source-less volume with mode", args: []string{"-v", "/tmp/hami-lite/cudevshr.cache:ro", "image"}, want: "volume target"},
		{name: "tmpfs", args: []string{"--tmpfs=/tmp/hami-lite:rw", "image"}, want: "tmpfs target"},
		{name: "label", args: []string{"--label", "io.hami.lite.vendor=user", "image"}, want: "managed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := testRequest(t, append([]string{"docker", "run"}, test.args...))
			_, err := New().Inject(t.Context(), request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Inject() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestInjectAcceptsEnabledNoNewPrivilegesForms(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"no-new-privileges",
		"no-new-privileges=true",
		"no-new-privileges:true",
	} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			request := testRequest(t, []string{"docker", "run", "--security-opt", value, "image"})
			if _, err := New().Inject(t.Context(), request); err != nil {
				t.Fatalf("Inject() error = %v", err)
			}
		})
	}
}

func TestInjectInspectsEnvAndLabelFiles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		flag    string
		content string
		want    string
	}{
		{name: "env file", flag: "--env-file", content: "SAFE=1\nLD_PRELOAD=/bad.so\n", want: "LD_PRELOAD"},
		{name: "env file disables control", flag: "--env-file", content: "SAFE=1\nCUDA_DISABLE_CONTROL=true\n", want: "CUDA_DISABLE_CONTROL"},
		{name: "env file disables core policy", flag: "--env-file", content: "SAFE=1\nGPU_CORE_UTILIZATION_POLICY=disable\n", want: "GPU_CORE_UTILIZATION_POLICY"},
		{name: "env file with BOM", flag: "--env-file", content: "\uFEFFLD_PRELOAD=/bad.so\n", want: "LD_PRELOAD"},
		{name: "env file capabilities", flag: "--env-file", content: "NVIDIA_DRIVER_CAPABILITIES=compute\n", want: "compute and utility"},
		{name: "label file", flag: "--label-file", content: "safe=true\nio.hami.lite.managed=false\n", want: "io.hami.lite.managed"},
		{name: "label file bare key", flag: "--label-file", content: "safe=true\nio.hami.lite.managed\n", want: "io.hami.lite.managed"},
		{name: "label file with BOM", flag: "--label-file", content: "\uFEFFio.hami.lite.managed=false\n", want: "io.hami.lite.managed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			filename := filepath.Join(t.TempDir(), "values")
			if err := os.WriteFile(filename, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			request := testRequest(t, []string{"docker", "run", test.flag, filename, "image"})
			_, err := New().Inject(t.Context(), request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Inject() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestInjectRejectsNonRegularAssignmentFile(t *testing.T) {
	t.Parallel()
	request := testRequest(t, []string{"docker", "run", "--env-file", os.DevNull, "image"})
	_, err := New().Inject(t.Context(), request)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("Inject() error = %v, want regular file rejection", err)
	}
}

func TestInjectPreservesNonConflictingOptionsAndContainerArgs(t *testing.T) {
	t.Parallel()
	request := testRequest(t, []string{
		"docker", "run", "--runtime=nvidia", "--privileged=false", "--disable-content-trust", "-i=false",
		"--future-flag=value", "--security-opt", "no-new-privileges=true", "--env", "SAFE=value",
		"--device-cgroup-rule", "c 1:3 rw",
		"--net", "host",
		"--env", "NVIDIA_DRIVER_CAPABILITIES=all",
		"--mount", "type=bind,src=/tmp,target=/tmp/hami-lite,dst=/workspace",
		"--volume", "/workspace-cache:ro", "image",
		"program", "--env", "LD_PRELOAD=/command-argument", "--gpus", "literal",
	})
	if _, err := New().Inject(t.Context(), request); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
}

func TestInjectRequiresEphemeralForegroundContainer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing remove", args: []string{"docker", "run", "image"}, want: "--rm"},
		{name: "detached", args: []string{"docker", "run", "--rm", "--detach", "image"}, want: "foreground"},
		{name: "restartable", args: []string{"docker", "run", "--rm", "--restart", "always", "image"}, want: "restart"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := testRequest(t, []string{"docker", "run", "image"})
			request.DockerArgs = test.args
			_, err := New().Inject(t.Context(), request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Inject() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestInjectVerifiesCoreDigest(t *testing.T) {
	t.Parallel()
	request := testRequest(t, []string{"docker", "run", "image"})
	request.Core.SHA256 = strings.Repeat("0", 64)
	_, err := New().Inject(t.Context(), request)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("Inject() error = %v, want digest mismatch", err)
	}
}

func TestInjectRejectsNonELFCore(t *testing.T) {
	t.Parallel()
	content := []byte("not an ELF shared object")
	path := filepath.Join(t.TempDir(), "libvgpu.so")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	request := testRequest(t, []string{"docker", "run", "image"})
	request.Core.Path = path
	request.Core.SHA256 = fmt.Sprintf("%x", digest)
	_, err := New().Inject(t.Context(), request)
	if err == nil || !strings.Contains(err.Error(), "not an ELF shared library") {
		t.Fatalf("Inject() error = %v, want ELF rejection", err)
	}
}

func TestInjectRejectsHeaderOnlyELFCore(t *testing.T) {
	t.Parallel()
	request := testRequest(t, []string{"docker", "run", "image"})
	content := testELFHeaderOnly(t)
	request.Core.Path = filepath.Join(t.TempDir(), "libvgpu.so")
	if err := os.WriteFile(request.Core.Path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	request.Core.SHA256 = fmt.Sprintf("%x", digest)
	_, err := New().Inject(t.Context(), request)
	if err == nil || !strings.Contains(err.Error(), "loadable shared library") {
		t.Fatalf("Inject() error = %v, want loadable shared library rejection", err)
	}
}

func TestInjectRejectsPIECore(t *testing.T) {
	t.Parallel()
	content := testELFPIE(t)
	request := testRequest(t, []string{"docker", "run", "image"})
	request.Core.Path = filepath.Join(t.TempDir(), "core-pie")
	if err := os.WriteFile(request.Core.Path, content, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	request.Core.SHA256 = fmt.Sprintf("%x", digest)
	_, err := New().Inject(t.Context(), request)
	if err == nil || !strings.Contains(err.Error(), "PIE executable") {
		t.Fatalf("Inject() error = %v, want PIE rejection", err)
	}
}

func TestInjectRejectsStaticPIECore(t *testing.T) {
	t.Parallel()
	content := testELFStaticPIE(t)
	request := testRequest(t, []string{"docker", "run", "image"})
	request.Core.Path = filepath.Join(t.TempDir(), "core-static-pie")
	if err := os.WriteFile(request.Core.Path, content, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	request.Core.SHA256 = fmt.Sprintf("%x", digest)
	_, err := New().Inject(t.Context(), request)
	if err == nil || !strings.Contains(err.Error(), "static PIE executable") {
		t.Fatalf("Inject() error = %v, want static PIE rejection", err)
	}
}

func TestValidateDeviceRejectsEmptyGPUUUID(t *testing.T) {
	t.Parallel()
	if _, err := validateDevice("GPU-"); err == nil {
		t.Fatal("validateDevice(\"GPU-\") succeeded")
	}
}

func TestInjectRunsHostGPUPreflightOutsideDryRun(t *testing.T) {
	t.Parallel()
	inspector := &fakeInspector{wantDevice: "GPU-abc"}
	endpoint := &fakeDockerEndpoint{}
	plugin := &Plugin{
		goos: "linux", goarch: "amd64", inspector: inspector, dockerEndpoint: endpoint,
		snapshotter: copyingSnapshotter{directory: t.TempDir()},
	}
	request := testRequest(t, []string{"docker", "run", "image"})
	request.Device = "GPU-abc"
	request.DryRun = false
	injection, err := plugin.Inject(t.Context(), request)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	defer injection.Close()
	if !inspector.called {
		t.Fatal("GPU inspector was not called")
	}
	if !endpoint.called {
		t.Fatal("Docker endpoint inspector was not called")
	}
}

func TestInjectRejectsRemoteDockerEndpoint(t *testing.T) {
	t.Parallel()
	endpoint := &fakeDockerEndpoint{err: fmt.Errorf("remote endpoint")}
	inspector := &fakeInspector{wantDevice: "0"}
	plugin := &Plugin{
		goos: "linux", goarch: "amd64", inspector: inspector, dockerEndpoint: endpoint,
		snapshotter: copyingSnapshotter{directory: t.TempDir()},
	}
	request := testRequest(t, []string{"docker", "run", "image"})
	request.DryRun = false
	_, err := plugin.Inject(t.Context(), request)
	if err == nil || !strings.Contains(err.Error(), "remote endpoint") {
		t.Fatalf("Inject() error = %v, want remote endpoint rejection", err)
	}
	if inspector.called {
		t.Fatal("GPU inspector ran after Docker endpoint rejection")
	}
}

func TestDockerEndpointRejectsDockerHostOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell shim")
	}
	directory := t.TempDir()
	docker := filepath.Join(directory, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\nprintf 'unix:///var/run/docker.sock\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", "tcp://remote.example:2375")
	t.Setenv("DOCKER_CONTEXT", "")
	err := (dockerContextInspector{}).ValidateLocal(t.Context())
	if err == nil || !strings.Contains(err.Error(), "tcp://remote.example:2375") {
		t.Fatalf("ValidateLocal() error = %v, want effective DOCKER_HOST rejection", err)
	}
}

func TestDockerEndpointContextOverridesDockerHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell shim")
	}
	directory := t.TempDir()
	docker := filepath.Join(directory, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\nprintf 'unix:///var/run/docker.sock\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", "tcp://remote.example:2375")
	t.Setenv("DOCKER_CONTEXT", "local-test")
	if err := (dockerContextInspector{}).ValidateLocal(t.Context()); err != nil {
		t.Fatalf("ValidateLocal() error = %v, want DOCKER_CONTEXT to override DOCKER_HOST", err)
	}
}

func TestInjectPinsVerifiedCoreForExecution(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	plugin := &Plugin{
		goos: "linux", goarch: "amd64",
		inspector:      &fakeInspector{wantDevice: "0"},
		dockerEndpoint: &fakeDockerEndpoint{},
		snapshotter:    copyingSnapshotter{directory: directory},
	}
	request := testRequest(t, []string{"docker", "run", "image"})
	request.DryRun = false
	original, err := os.ReadFile(request.Core.Path)
	if err != nil {
		t.Fatal(err)
	}
	injection, err := plugin.Inject(t.Context(), request)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	defer injection.Close()

	var snapshotPath string
	volumeSuffix := ":" + coreContainerPath + ":ro,Z"
	for index, argument := range injection.Args {
		if argument == "--volume" && index+1 < len(injection.Args) && strings.HasSuffix(injection.Args[index+1], volumeSuffix) {
			snapshotPath = strings.TrimSuffix(injection.Args[index+1], volumeSuffix)
			break
		}
	}
	if snapshotPath == "" || snapshotPath == request.Core.Path {
		t.Fatalf("Core mount source = %q, want pinned path distinct from %q", snapshotPath, request.Core.Path)
	}
	if err := os.WriteFile(request.Core.Path, []byte("replaced after verification"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("pinned Core bytes changed after the original path was replaced")
	}
}

func TestInjectUsesPrivateSELinuxLabelForExecutionSnapshot(t *testing.T) {
	t.Parallel()
	plugin := &Plugin{
		goos: "linux", goarch: "amd64",
		inspector:      &fakeInspector{wantDevice: "0"},
		dockerEndpoint: &fakeDockerEndpoint{},
		snapshotter:    copyingSnapshotter{directory: t.TempDir()},
	}
	request := testRequest(t, []string{"docker", "run", "image"})
	request.DryRun = false
	injection, err := plugin.Inject(t.Context(), request)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	defer injection.Close()

	for index, argument := range injection.Args {
		if argument == "--volume" && index+1 < len(injection.Args) {
			if !strings.HasSuffix(injection.Args[index+1], ":"+coreContainerPath+":ro,Z") {
				t.Fatalf("Core mount = %q, want private SELinux label for unique execution snapshot", injection.Args[index+1])
			}
			return
		}
	}
	t.Fatal("injection is missing Core volume")
}

func TestInjectPinsValidatedAssignmentFilesForExecution(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	environmentPath := filepath.Join(directory, "environment")
	labelPath := filepath.Join(directory, "labels")
	if err := os.WriteFile(environmentPath, []byte("SAFE=before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(labelPath, []byte("team=hami\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plugin := &Plugin{
		goos: "linux", goarch: "amd64",
		inspector:      &fakeInspector{wantDevice: "0"},
		dockerEndpoint: &fakeDockerEndpoint{},
		snapshotter:    copyingSnapshotter{directory: directory},
	}
	request := testRequest(t, []string{
		"docker", "run", "--env-file", environmentPath,
		"--label-file=" + labelPath, "image",
	})
	request.DryRun = false
	originalArgs := slices.Clone(request.DockerArgs)
	injection, err := plugin.Inject(t.Context(), request)
	if err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	defer injection.Close()

	pinnedEnvironment := dockerOptionValue(t, injection.DockerArgs, "--env-file")
	pinnedLabels := dockerOptionValue(t, injection.DockerArgs, "--label-file")
	if pinnedEnvironment == environmentPath || pinnedLabels == labelPath {
		t.Fatalf("assignment paths were not pinned: env=%q labels=%q", pinnedEnvironment, pinnedLabels)
	}
	if !slices.Equal(request.DockerArgs, originalArgs) {
		t.Fatalf("Inject() mutated request argv: got %#v, want %#v", request.DockerArgs, originalArgs)
	}
	if err := os.WriteFile(environmentPath, []byte("LD_PRELOAD=/bad.so\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(labelPath, []byte("io.hami.lite.managed=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gotEnvironment, err := os.ReadFile(pinnedEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	gotLabels, err := os.ReadFile(pinnedLabels)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotEnvironment) != "SAFE=before\n" || string(gotLabels) != "team=hami\n" {
		t.Fatalf("pinned assignments changed: env=%q labels=%q", gotEnvironment, gotLabels)
	}
}

func dockerOptionValue(t *testing.T, args []string, name string) string {
	t.Helper()
	for index, argument := range args {
		if argument == name && index+1 < len(args) {
			return args[index+1]
		}
		if value, found := strings.CutPrefix(argument, name+"="); found {
			return value
		}
	}
	t.Fatalf("Docker option %s not found in %#v", name, args)
	return ""
}

func TestParseMemory(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  uint64
	}{
		{value: "1024", want: 1024},
		{value: "8GiB", want: 8 << 30},
		{value: "512MiB", want: 512 << 20},
		{value: "4KiB", want: 4 << 10},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			got, err := parseMemory(test.value)
			if err != nil || got != test.want {
				t.Fatalf("parseMemory(%q) = %d, %v; want %d", test.value, got, err, test.want)
			}
		})
	}
	for _, value := range []string{"", "0", "1.5GiB", "8Gi", "8GB", "8MB", "8KB", "4g", "4G", "8B"} {
		t.Run("invalid "+value, func(t *testing.T) {
			t.Parallel()
			if _, err := parseMemory(value); err == nil {
				t.Fatalf("parseMemory(%q) succeeded", value)
			}
		})
	}
}

func TestParseDockerOptionsRequiresKnownCurrentDockerFlags(t *testing.T) {
	t.Parallel()
	_, err := parseDockerOptions([]string{"--future-flag", "value", "image"})
	if err == nil || !strings.Contains(err.Error(), "unknown Docker option") {
		t.Fatalf("parseDockerOptions() error = %v", err)
	}
}
