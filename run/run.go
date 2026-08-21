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

// Package run defines the shared contract for vendor-specific Docker command
// injection.
package run

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
)

// Artifact identifies the exact HAMi Core library injected into a container.
type Artifact struct {
	Path    string
	Version string
	SHA256  string
}

// Request contains the shared command and resource parameters accepted by the
// first vendor plugin contract.
type Request struct {
	DockerArgs []string
	Device     string
	Memory     string
	Compute    int
	Core       Artifact
	DryRun     bool
}

// Plugin contributes vendor-specific Docker run arguments.
type Plugin interface {
	Name() string
	Inject(context.Context, Request) (Injection, error)
}

// Injection contains vendor arguments, an optional rewritten Docker command,
// and resources that must remain alive until Docker has consumed the command.
type Injection struct {
	// Args are inserted immediately after "docker run".
	Args []string
	// DockerArgs replaces the request command when a plugin pins referenced files.
	DockerArgs []string
	// Cleanup releases resources retained by Args or DockerArgs.
	Cleanup func()
}

// Close releases resources retained by the injection.
func (injection *Injection) Close() {
	if injection.Cleanup == nil {
		return
	}
	injection.Cleanup()
	injection.Cleanup = nil
}

// PreparedCommand is a Docker argv plus resources retained until execution.
type PreparedCommand struct {
	Argv      []string
	injection Injection
}

// Close releases resources retained during command preparation.
func (command *PreparedCommand) Close() {
	command.injection.Close()
}

// Runner replaces the current process with the prepared Docker command.
type Runner interface {
	Exec(context.Context, []string) error
}

// ExecRunner uses exec(2), so Docker receives the original standard streams,
// signals, and process exit status without an intermediate wrapper process.
type ExecRunner struct{}

// Exec implements Runner.
func (ExecRunner) Exec(_ context.Context, argv []string) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("find %q: %w", argv[0], err)
	}
	if err := syscall.Exec(path, argv, os.Environ()); err != nil {
		return fmt.Errorf("execute %q: %w", argv[0], err)
	}
	return nil
}

// Prepare validates the shared command contract and inserts plugin arguments
// immediately after "docker run" without changing the user's argv values or
// relative order.
func Prepare(ctx context.Context, vendor string, plugins []Plugin, request Request) (*PreparedCommand, error) {
	if len(request.DockerArgs) < 3 || request.DockerArgs[0] != "docker" || request.DockerArgs[1] != "run" {
		return nil, errors.New("command after -- must match: docker run [OPTIONS] IMAGE [COMMAND] [ARG...]")
	}

	var selected Plugin
	for _, plugin := range plugins {
		if plugin.Name() != vendor {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("vendor plugin %q is registered more than once", vendor)
		}
		selected = plugin
	}
	if selected == nil {
		available := make([]string, 0, len(plugins))
		for _, plugin := range plugins {
			available = append(available, plugin.Name())
		}
		slices.Sort(available)
		return nil, fmt.Errorf("unsupported vendor %q (available: %s)", vendor, strings.Join(available, ", "))
	}

	injection, err := selected.Inject(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("prepare %s runtime: %w", vendor, err)
	}
	dockerArgs := request.DockerArgs
	if injection.DockerArgs != nil {
		dockerArgs = injection.DockerArgs
		if len(dockerArgs) < 3 || dockerArgs[0] != "docker" || dockerArgs[1] != "run" {
			injection.Close()
			return nil, fmt.Errorf("plugin %q returned an invalid Docker command", vendor)
		}
	}
	argv := make([]string, 0, len(dockerArgs)+len(injection.Args))
	argv = append(argv, dockerArgs[:2]...)
	argv = append(argv, injection.Args...)
	argv = append(argv, dockerArgs[2:]...)
	return &PreparedCommand{Argv: argv, injection: injection}, nil
}

// Execute prepares the command, prints it for dry runs, or replaces the current
// process through runner.
func Execute(ctx context.Context, out io.Writer, runner Runner, vendor string, plugins []Plugin, request Request) error {
	prepared, err := Prepare(ctx, vendor, plugins, request)
	if err != nil {
		return err
	}
	defer prepared.Close()
	if request.DryRun {
		_, err := fmt.Fprintln(out, ShellQuote(prepared.Argv))
		return err
	}
	return runner.Exec(ctx, prepared.Argv)
}

// ShellQuote formats argv as one POSIX-shell command without changing any
// argument value when the result is parsed by the shell.
func ShellQuote(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = quote(arg)
	}
	return strings.Join(quoted, " ")
}

func quote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || strings.ContainsRune("_@%+=:,./-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
