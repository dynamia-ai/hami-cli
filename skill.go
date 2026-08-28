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
	"embed"

	kitup "github.com/lathe-cli/kitup/go"
	kitupcobra "github.com/lathe-cli/kitup/go-cobra"
	"github.com/spf13/cobra"
)

const (
	resourcePoolSkillName = "managing-hami-resource-pools"
	resourcePoolSkillRoot = "skills/" + resourcePoolSkillName
)

//go:embed skills/managing-hami-resource-pools
var embeddedSkills embed.FS

func newSkillCommand(options *commandOptions) *cobra.Command {
	return kitupcobra.NewSkillCommand(kitupcobra.Options{
		AppID:        "hami-cli",
		SkillName:    resourcePoolSkillName,
		Bundle:       kitup.FSBundle(embeddedSkills, resourcePoolSkillRoot),
		DefaultScope: kitup.UserScope,
		Home:         options.kitupHome,
		CWD:          options.kitupCWD,
		StdinTTY:     options.stdinTTY,
		Out:          options.out,
		Err:          options.errOut,
	})
}
