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
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
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

const (
	elfClass64          = 2
	elfDataLittleEndian = 1
)

func testRequest(t *testing.T, dockerArgs []string) hamirun.Request {
	t.Helper()
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
	joined := strings.Join(injection, "\n")
	for _, want := range []string{
		"--gpus\ndevice=0",
		"dst=/usr/local/vgpu/libvgpu.so",
		"type=tmpfs,dst=/tmp/hami-lite,tmpfs-mode=1777",
		"type=tmpfs,dst=/tmp/vgpulock,tmpfs-mode=1777",
		"LD_PRELOAD=/usr/local/vgpu/libvgpu.so",
		"CUDA_REDIRECT=/usr/local/vgpu/libvgpu.so",
		"CUDA_DEVICE_MEMORY_LIMIT=8589934592",
		"CUDA_DEVICE_SM_LIMIT=50",
		"CUDA_DEVICE_MEMORY_SHARED_CACHE=/tmp/hami-lite/cudevshr.cache",
		"NVIDIA_DRIVER_CAPABILITIES=compute,utility",
		"io.hami.lite.core.version=v2.7.0-test",
		"io.hami.lite.core.sha256=" + request.Core.SHA256,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("injection missing %q:\n%s", want, joined)
		}
	}
	if slices.Contains(injection, request.DockerArgs[2]) {
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
		{name: "NVIDIA device", args: []string{"--device", "/dev/nvidia0", "image"}, want: "--device"},
		{name: "environment", args: []string{"--env", "LD_PRELOAD=/bad.so", "image"}, want: "LD_PRELOAD"},
		{name: "short environment", args: []string{"-eCUDA_DEVICE_SM_LIMIT=90", "image"}, want: "CUDA_DEVICE_SM_LIMIT"},
		{name: "per-device environment", args: []string{"-e", "CUDA_DEVICE_MEMORY_LIMIT_0=1g", "image"}, want: "CUDA_DEVICE_MEMORY_LIMIT_0"},
		{name: "driver capabilities", args: []string{"-e", "NVIDIA_DRIVER_CAPABILITIES=video,utility", "image"}, want: "compute and utility"},
		{name: "mount", args: []string{"--mount", "type=bind,src=/tmp,dst=/usr/local/vgpu", "image"}, want: "mount target"},
		{name: "NVIDIA device mount", args: []string{"--mount", "type=bind,src=/dev,dst=/dev", "image"}, want: "mount target"},
		{name: "volume", args: []string{"-v", "/tmp:/tmp/vgpulock", "image"}, want: "volume target"},
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

func TestInjectInspectsEnvAndLabelFiles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		flag    string
		content string
		want    string
	}{
		{name: "env file", flag: "--env-file", content: "SAFE=1\nLD_PRELOAD=/bad.so\n", want: "LD_PRELOAD"},
		{name: "env file with BOM", flag: "--env-file", content: "\uFEFFLD_PRELOAD=/bad.so\n", want: "LD_PRELOAD"},
		{name: "env file capabilities", flag: "--env-file", content: "NVIDIA_DRIVER_CAPABILITIES=compute\n", want: "compute and utility"},
		{name: "label file", flag: "--label-file", content: "safe=true\nio.hami.lite.managed=false\n", want: "io.hami.lite.managed"},
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

func TestInjectPreservesNonConflictingOptionsAndContainerArgs(t *testing.T) {
	t.Parallel()
	request := testRequest(t, []string{
		"docker", "run", "--runtime=nvidia", "--privileged=false", "-i=false",
		"--future-flag=value", "--device", "/dev/snd", "--env", "SAFE=value",
		"--env", "NVIDIA_DRIVER_CAPABILITIES=all",
		"--mount", "type=bind,src=/tmp,dst=/workspace", "image",
		"program", "--env", "LD_PRELOAD=/command-argument", "--gpus", "literal",
	})
	if _, err := New().Inject(t.Context(), request); err != nil {
		t.Fatalf("Inject() error = %v", err)
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
	plugin := &Plugin{goos: "linux", goarch: "amd64", inspector: inspector, dockerEndpoint: endpoint}
	request := testRequest(t, []string{"docker", "run", "image"})
	request.Device = "GPU-abc"
	request.DryRun = false
	if _, err := plugin.Inject(t.Context(), request); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
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
	plugin := &Plugin{goos: "linux", goarch: "amd64", inspector: inspector, dockerEndpoint: endpoint}
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

func TestParseMemory(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  uint64
	}{
		{value: "1024", want: 1024},
		{value: "8GiB", want: 8 << 30},
		{value: "512MiB", want: 512 << 20},
		{value: "4g", want: 4 << 30},
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
	for _, value := range []string{"", "0", "1.5GiB", "8Gi"} {
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
