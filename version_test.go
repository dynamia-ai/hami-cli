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
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolveCLIBuildInfoUsesStandaloneModuleVersion(t *testing.T) {
	t.Parallel()
	buildInfo := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.0.0-20260817092328-9214a562258d"},
	}

	got := resolveCLIBuildInfo("devel", "unknown", "unknown", buildInfo)
	if got.Version != "v0.0.0-20260817092328-9214a562258d" {
		t.Errorf("Version = %q, want module version", got.Version)
	}
	if got.Revision != "9214a562258d" {
		t.Errorf("Revision = %q", got.Revision)
	}
	if got.BuildDate != "2026-08-17T09:23:28Z" {
		t.Errorf("BuildDate = %q", got.BuildDate)
	}
}

func TestResolveCLIBuildInfoPrefersVCSSettings(t *testing.T) {
	t.Parallel()
	buildInfo := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.0.0-20260817092328-9214a562258d"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "9214a562258deb2850bd16a199950ef9c5568578"},
			{Key: "vcs.time", Value: "2026-08-17T09:24:00Z"},
		},
	}

	got := resolveCLIBuildInfo("devel", "unknown", "unknown", buildInfo)
	if got.Revision != "9214a562258deb2850bd16a199950ef9c5568578" {
		t.Errorf("Revision = %q", got.Revision)
	}
	if got.BuildDate != "2026-08-17T09:24:00Z" {
		t.Errorf("BuildDate = %q", got.BuildDate)
	}
}

func TestResolveCLIBuildInfoPreservesReleaseMetadata(t *testing.T) {
	t.Parallel()
	buildInfo := &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260817092328-9214a562258d"}}

	got := resolveCLIBuildInfo("latest", "abc123", "2026-08-17T10:00:00Z", buildInfo)
	if got.Version != "latest" || got.Revision != "abc123" || got.BuildDate != "2026-08-17T10:00:00Z" {
		t.Fatalf("resolveCLIBuildInfo() = %#v", got)
	}
	if output := got.String(); !strings.Contains(output, "version:          latest") {
		t.Errorf("String() = %q", output)
	}
}

func TestResolveCLIBuildInfoUsesTaggedModuleVersion(t *testing.T) {
	t.Parallel()
	buildInfo := &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}

	got := resolveCLIBuildInfo("devel", "unknown", "unknown", buildInfo)
	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", got.Version)
	}
}
