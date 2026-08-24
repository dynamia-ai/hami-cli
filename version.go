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
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

var (
	cliVersion   = "devel"
	cliRevision  = "unknown"
	cliBuildDate = "unknown"
)

const resourcePoolAPIModulePath = "github.com/dynamia-ai/hami-enterprise/pkg/resourcepool/api"

type cliBuildInfo struct {
	Version                 string
	Revision                string
	ResourcePoolAPIRevision string
	CommitDate              string
	BuildDate               string
	GoVersion               string
	Compiler                string
	Platform                string
}

func currentCLIBuildInfo() cliBuildInfo {
	buildInfo, _ := debug.ReadBuildInfo()
	return resolveCLIBuildInfo(cliVersion, cliRevision, cliBuildDate, buildInfo)
}

func resolveCLIBuildInfo(version, revision, buildDate string, buildInfo *debug.BuildInfo) cliBuildInfo {
	commitDate := "unknown"
	if buildInfo != nil {
		if version == "devel" && buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" {
			version = buildInfo.Main.Version
		}
		for _, setting := range buildInfo.Settings {
			switch setting.Key {
			case "vcs.revision":
				if revision == "unknown" && setting.Value != "" {
					revision = setting.Value
				}
			case "vcs.time":
				if commitDate == "unknown" && setting.Value != "" {
					commitDate = setting.Value
				}
			}
		}
		if revision == "unknown" || commitDate == "unknown" {
			moduleRevision, moduleCommitDate := parsePseudoVersion(buildInfo.Main.Version)
			if revision == "unknown" && moduleRevision != "" {
				revision = moduleRevision
			}
			if commitDate == "unknown" && moduleCommitDate != "" {
				commitDate = moduleCommitDate
			}
		}
	}

	return cliBuildInfo{
		Version:                 version,
		Revision:                revision,
		ResourcePoolAPIRevision: dependencyRevision(buildInfo, resourcePoolAPIModulePath),
		CommitDate:              commitDate,
		BuildDate:               buildDate,
		GoVersion:               runtime.Version(),
		Compiler:                runtime.Compiler,
		Platform:                fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

func dependencyRevision(buildInfo *debug.BuildInfo, modulePath string) string {
	if buildInfo == nil {
		return "unknown"
	}
	for _, dependency := range buildInfo.Deps {
		if dependency.Path != modulePath {
			continue
		}
		if dependency.Replace != nil {
			dependency = dependency.Replace
		}
		revision, _ := parsePseudoVersion(dependency.Version)
		if revision != "" {
			return revision
		}
		return "unknown"
	}
	return "unknown"
}

func parsePseudoVersion(version string) (string, string) {
	parts := strings.Split(version, "-")
	if len(parts) < 3 {
		return "", ""
	}
	timestamp := parts[len(parts)-2]
	revision := parts[len(parts)-1]
	if len(timestamp) != len("20060102150405") || len(revision) != 12 {
		return "", ""
	}
	parsedTime, err := time.Parse("20060102150405", timestamp)
	if err != nil {
		return "", ""
	}
	return revision, parsedTime.UTC().Format(time.RFC3339)
}

func (info cliBuildInfo) String() string {
	return fmt.Sprintf(`version:          %s
revision:         %s
resource pool API: %s
commit date:      %s
build date:       %s
go version:       %s
compiler:         %s
platform:         %s`, info.Version, info.Revision, info.ResourcePoolAPIRevision, info.CommitDate, info.BuildDate, info.GoVersion, info.Compiler, info.Platform)
}
