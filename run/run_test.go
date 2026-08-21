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

package run

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

type fakePlugin struct {
	name      string
	injection []string
	err       error
}

func (plugin fakePlugin) Name() string { return plugin.name }

func (plugin fakePlugin) Inject(_ context.Context, _ Request) (Injection, error) {
	return Injection{Args: plugin.injection}, plugin.err
}

type fakeRunner struct {
	argv []string
}

func (runner *fakeRunner) Exec(_ context.Context, argv []string) error {
	runner.argv = append([]string(nil), argv...)
	return nil
}

func TestPreparePreservesDockerArgv(t *testing.T) {
	t.Parallel()
	original := []string{
		"docker", "run", "--rm", "-it", "--name", "training job",
		"-v", "/tmp/a path:/workspace", "--network", "host",
		"pytorch/pytorch:latest", "python", "train.py", "--message", "it's exact", "",
	}
	injection := []string{"--env", "HAMi=value with spaces", "--label", "io.hami.test=true"}
	prepared, err := Prepare(t.Context(), "test", []Plugin{fakePlugin{name: "test", injection: injection}}, Request{DockerArgs: original})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	defer prepared.Close()
	want := append([]string{"docker", "run"}, injection...)
	want = append(want, original[2:]...)
	if !reflect.DeepEqual(prepared.Argv, want) {
		t.Fatalf("Prepare() = %#v, want %#v", prepared.Argv, want)
	}
	if !reflect.DeepEqual(prepared.Argv[len(injection)+2:], original[2:]) {
		t.Fatalf("user argv changed: got %#v, want %#v", prepared.Argv[len(injection)+2:], original[2:])
	}
}

func TestExecuteCleansUpPreparedResources(t *testing.T) {
	t.Parallel()
	cleaned := false
	plugin := cleanupPlugin{cleanup: func() { cleaned = true }}
	if err := Execute(t.Context(), &bytes.Buffer{}, &fakeRunner{}, "test", []Plugin{plugin}, Request{DockerArgs: []string{"docker", "run", "image"}}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !cleaned {
		t.Fatal("Execute() did not clean up retained plugin resources after runner returned")
	}
}

type cleanupPlugin struct {
	cleanup func()
}

func (cleanupPlugin) Name() string { return "test" }

func (plugin cleanupPlugin) Inject(context.Context, Request) (Injection, error) {
	return Injection{Cleanup: plugin.cleanup}, nil
}

func TestPrepareRejectsInvalidCommandAndVendor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		vendor    string
		plugins   []Plugin
		dockerArg []string
		want      string
	}{
		{name: "not docker run", vendor: "test", plugins: []Plugin{fakePlugin{name: "test"}}, dockerArg: []string{"docker", "exec", "image"}, want: "docker run"},
		{name: "unknown vendor", vendor: "ascend", plugins: []Plugin{fakePlugin{name: "nvidia"}}, dockerArg: []string{"docker", "run", "image"}, want: "available: nvidia"},
		{name: "duplicate vendor", vendor: "nvidia", plugins: []Plugin{fakePlugin{name: "nvidia"}, fakePlugin{name: "nvidia"}}, dockerArg: []string{"docker", "run", "image"}, want: "registered more than once"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Prepare(t.Context(), test.vendor, test.plugins, Request{DockerArgs: test.dockerArg})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Prepare() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestExecuteDryRunUsesCopyableShellQuoting(t *testing.T) {
	t.Parallel()
	output := &bytes.Buffer{}
	runner := &fakeRunner{}
	err := Execute(t.Context(), output, runner, "test", []Plugin{fakePlugin{
		name:      "test",
		injection: []string{"--env", "MESSAGE=it's exact"},
	}}, Request{
		DockerArgs: []string{"docker", "run", "--rm", "image", "printf", "%s", "", "$HOME"},
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := "docker run --env 'MESSAGE=it'\"'\"'s exact' --rm image printf %s '' '$HOME'\n"
	if output.String() != want {
		t.Fatalf("dry-run output = %q, want %q", output.String(), want)
	}
	if runner.argv != nil {
		t.Fatalf("dry run executed %#v", runner.argv)
	}
}

func TestExecuteDelegatesPreparedArgv(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{}
	err := Execute(t.Context(), &bytes.Buffer{}, runner, "test", []Plugin{fakePlugin{
		name:      "test",
		injection: []string{"--env", "A=B"},
	}}, Request{DockerArgs: []string{"docker", "run", "image"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []string{"docker", "run", "--env", "A=B", "image"}
	if !reflect.DeepEqual(runner.argv, want) {
		t.Fatalf("runner argv = %#v, want %#v", runner.argv, want)
	}
}

func TestExecuteReturnsPluginError(t *testing.T) {
	t.Parallel()
	want := errors.New("broken")
	err := Execute(t.Context(), &bytes.Buffer{}, &fakeRunner{}, "test", []Plugin{fakePlugin{name: "test", err: want}}, Request{DockerArgs: []string{"docker", "run", "image"}})
	if !errors.Is(err, want) {
		t.Fatalf("Execute() error = %v, want wrapped %v", err, want)
	}
}

func TestExecRunnerPreservesProcessStatus(t *testing.T) {
	if mode := os.Getenv("HAMI_CLI_EXEC_HELPER"); mode != "" {
		command := "exit 37"
		if mode == "signal" {
			command = "kill -TERM $$"
		}
		if err := (ExecRunner{}).Exec(t.Context(), []string{"/bin/sh", "-c", command}); err != nil {
			t.Fatalf("ExecRunner.Exec() error = %v", err)
		}
		t.Fatal("ExecRunner.Exec() returned after successful exec")
	}

	tests := []struct {
		name     string
		mode     string
		exitCode int
		signal   syscall.Signal
	}{
		{name: "exit code", mode: "exit", exitCode: 37},
		{name: "signal", mode: "signal", signal: syscall.SIGTERM},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestExecRunnerPreservesProcessStatus$")
			command.Env = append(os.Environ(), "HAMI_CLI_EXEC_HELPER="+test.mode)
			err := command.Run()
			exitError, ok := errors.AsType[*exec.ExitError](err)
			if !ok {
				t.Fatalf("helper error = %v, want *exec.ExitError", err)
			}
			status, ok := exitError.Sys().(syscall.WaitStatus)
			if !ok {
				t.Fatalf("helper status type = %T, want syscall.WaitStatus", exitError.Sys())
			}
			if test.signal != 0 {
				if !status.Signaled() || status.Signal() != test.signal {
					t.Fatalf("helper wait status = %v, want signal %s", status, test.signal)
				}
				return
			}
			if status.ExitStatus() != test.exitCode {
				t.Fatalf("helper exit code = %d, want %d", status.ExitStatus(), test.exitCode)
			}
		})
	}
}
