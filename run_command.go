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
	"errors"

	"github.com/dynamia-ai/hami-cli/nvidia"
	hamirun "github.com/dynamia-ai/hami-cli/run"
	"github.com/spf13/cobra"
)

func newRunCommand(options *commandOptions) *cobra.Command {
	var (
		vendor      string
		device      string
		memory      string
		compute     int
		corePath    string
		coreVersion string
		coreSHA256  string
		dryRun      bool
	)

	command := &cobra.Command{
		Use:   "run --vendor VENDOR [HAMi options] -- docker run [Docker options] IMAGE [COMMAND] [ARG...]",
		Short: "Run one local Docker container with a vendor HAMi Core",
		Args: func(command *cobra.Command, args []string) error {
			argsBeforeDash := command.ArgsLenAtDash()
			if argsBeforeDash < 0 {
				return errors.New("a literal -- must separate HAMi options from docker run")
			}
			if argsBeforeDash != 0 {
				return errors.New("only HAMi flags are allowed before --")
			}
			if len(args) == 0 {
				return errors.New("docker run command is required after --")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			plugins := options.runPlugins
			if plugins == nil {
				plugins = []hamirun.Plugin{nvidia.New()}
			}
			runner := options.runRunner
			if runner == nil {
				runner = hamirun.ExecRunner{}
			}
			return hamirun.Execute(command.Context(), options.out, runner, vendor, plugins, hamirun.Request{
				DockerArgs: args,
				Device:     device,
				Memory:     memory,
				Compute:    compute,
				Core: hamirun.Artifact{
					Path:    corePath,
					Version: coreVersion,
					SHA256:  coreSHA256,
				},
				DryRun: dryRun,
			})
		},
	}
	command.Flags().StringVar(&vendor, "vendor", "", "GPU vendor plugin (currently nvidia)")
	command.Flags().StringVar(&device, "device", "", "One NVIDIA GPU index or GPU UUID")
	command.Flags().StringVar(&memory, "memory", "", "GPU memory limit in bytes, KiB, MiB, or GiB")
	command.Flags().IntVar(&compute, "compute", 100, "GPU SM utilization limit from 1 to 100")
	command.Flags().StringVar(&corePath, "core", "", "Path to the HAMi Core libvgpu.so artifact")
	command.Flags().StringVar(&coreVersion, "core-version", "", "Version of the HAMi Core artifact")
	command.Flags().StringVar(&coreSHA256, "core-sha256", "", "Expected SHA-256 digest of the Core artifact")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Print the final shell-quoted Docker command without executing it")
	_ = command.MarkFlagRequired("vendor")
	_ = command.MarkFlagRequired("device")
	_ = command.MarkFlagRequired("memory")
	_ = command.MarkFlagRequired("core")
	_ = command.MarkFlagRequired("core-version")
	_ = command.MarkFlagRequired("core-sha256")
	return command
}
