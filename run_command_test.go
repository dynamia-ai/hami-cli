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

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	hamirun "github.com/dynamia-ai/hami-cli/run"
)

type commandTestPlugin struct{}

func (commandTestPlugin) Name() string { return "test" }

func (commandTestPlugin) Inject(_ context.Context, request hamirun.Request) ([]string, error) {
	return []string{"--env", "RESOURCE=" + request.Memory}, nil
}

type commandTestRunner struct {
	argv []string
}

func (runner *commandTestRunner) Exec(_ context.Context, argv []string) error {
	runner.argv = append([]string(nil), argv...)
	return nil
}

func TestRunCommandRequiresLiteralDashSeparator(t *testing.T) {
	t.Parallel()
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{out: output, errOut: output, runPlugins: []hamirun.Plugin{commandTestPlugin{}}})
	command.SetArgs([]string{
		"run", "--vendor", "test", "--device", "0", "--memory", "1GiB",
		"--core", "/tmp/libvgpu.so", "--core-version", "test", "--core-sha256", strings.Repeat("0", 64),
		"docker", "run", "image",
	})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "literal --") {
		t.Fatalf("Execute() error = %v, want separator error", err)
	}
}

func TestRunCommandDryRunPreservesDockerArgs(t *testing.T) {
	t.Parallel()
	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{out: output, errOut: output, runPlugins: []hamirun.Plugin{commandTestPlugin{}}})
	command.SetArgs([]string{
		"run", "--vendor", "test", "--device", "0", "--memory", "1GiB",
		"--core", "/tmp/libvgpu.so", "--core-version", "test", "--core-sha256", strings.Repeat("0", 64),
		"--dry-run", "--", "docker", "run", "--rm", "image", "command", "value with spaces",
	})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "docker run --env RESOURCE=1GiB --rm image command 'value with spaces'\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestRunCommandExecutesPreparedArgv(t *testing.T) {
	t.Parallel()
	runner := &commandTestRunner{}
	command := newRootCommand(&commandOptions{
		out:        &bytes.Buffer{},
		errOut:     &bytes.Buffer{},
		runPlugins: []hamirun.Plugin{commandTestPlugin{}},
		runRunner:  runner,
	})
	command.SetArgs([]string{
		"run", "--vendor", "test", "--device", "0", "--memory", "2GiB",
		"--core", "/tmp/libvgpu.so", "--core-version", "test", "--core-sha256", strings.Repeat("0", 64),
		"--", "docker", "run", "image", "command", "--flag",
	})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []string{"docker", "run", "--env", "RESOURCE=2GiB", "image", "command", "--flag"}
	if !reflect.DeepEqual(runner.argv, want) {
		t.Fatalf("runner argv = %#v, want %#v", runner.argv, want)
	}
}

func TestRunCommandNVIDIADryRun(t *testing.T) {
	t.Parallel()
	header := make([]byte, 64)
	copy(header, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	binary.LittleEndian.PutUint16(header[16:], 3)  // ET_DYN
	binary.LittleEndian.PutUint16(header[18:], 62) // EM_X86_64
	binary.LittleEndian.PutUint32(header[20:], 1)  // EV_CURRENT
	binary.LittleEndian.PutUint16(header[52:], 64)
	binary.LittleEndian.PutUint16(header[54:], 56)
	binary.LittleEndian.PutUint16(header[58:], 64)
	corePath := filepath.Join(t.TempDir(), "libvgpu.so")
	if err := os.WriteFile(corePath, header, 0o600); err != nil {
		t.Fatalf("write test Core: %v", err)
	}
	digest := sha256.Sum256(header)

	output := &bytes.Buffer{}
	command := newRootCommand(&commandOptions{out: output, errOut: output})
	command.SetArgs([]string{
		"run", "--vendor", "nvidia", "--device", "0", "--memory", "8GiB", "--compute", "50",
		"--core", corePath, "--core-version", "test", "--core-sha256", fmt.Sprintf("%x", digest),
		"--dry-run", "--", "docker", "run", "--rm", "image", "command", "value with spaces",
	})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"docker run --gpus device=0",
		"CUDA_DEVICE_MEMORY_LIMIT=8589934592",
		"NVIDIA_DRIVER_CAPABILITIES=compute,utility",
		"--rm image command 'value with spaces'",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("dry-run output missing %q:\n%s", want, output.String())
		}
	}
}
