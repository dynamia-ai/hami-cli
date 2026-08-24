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

func TestResolveCLIBuildInfo(t *testing.T) {
	t.Parallel()
	pseudoVersion := "v0.0.0-20260817092328-9214a562258d"
	tests := []struct {
		name      string
		version   string
		revision  string
		buildDate string
		settings  []debug.BuildSetting
		want      cliBuildInfo
	}{
		{
			name:      "pseudo-version metadata",
			version:   "devel",
			revision:  "unknown",
			buildDate: "unknown",
			want:      cliBuildInfo{Version: pseudoVersion, Revision: "9214a562258d", CommitDate: "2026-08-17T09:23:28Z", BuildDate: "unknown"},
		},
		{
			name:      "VCS metadata",
			version:   "devel",
			revision:  "unknown",
			buildDate: "unknown",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "9214a562258deb2850bd16a199950ef9c5568578"},
				{Key: "vcs.time", Value: "2026-08-17T09:24:00Z"},
			},
			want: cliBuildInfo{Version: pseudoVersion, Revision: "9214a562258deb2850bd16a199950ef9c5568578", CommitDate: "2026-08-17T09:24:00Z", BuildDate: "unknown"},
		},
		{
			name:      "release metadata",
			version:   "latest",
			revision:  "abc123",
			buildDate: "2026-08-17T10:00:00Z",
			want:      cliBuildInfo{Version: "latest", Revision: "abc123", CommitDate: "2026-08-17T09:23:28Z", BuildDate: "2026-08-17T10:00:00Z"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := resolveCLIBuildInfo(test.version, test.revision, test.buildDate, &debug.BuildInfo{
				Main:     debug.Module{Version: pseudoVersion},
				Settings: test.settings,
			})
			if got.Version != test.want.Version || got.Revision != test.want.Revision || got.CommitDate != test.want.CommitDate || got.BuildDate != test.want.BuildDate {
				t.Fatalf("resolveCLIBuildInfo() = %#v, want version metadata %#v", got, test.want)
			}
			if test.name == "release metadata" {
				output := got.String()
				if !strings.Contains(output, "version:          latest") ||
					!strings.Contains(output, "commit date:      2026-08-17T09:23:28Z") ||
					!strings.Contains(output, "build date:       2026-08-17T10:00:00Z") {
					t.Fatalf("String() = %q", output)
				}
			}
		})
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

func TestResolveCLIBuildInfoResourcePoolAPIRevision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		buildInfo *debug.BuildInfo
		want      string
	}{
		{
			name: "pseudo-version",
			buildInfo: &debug.BuildInfo{Deps: []*debug.Module{{
				Path:    resourcePoolAPIModulePath,
				Version: "v0.0.0-20260824104039-b85d4c6f1d21",
			}}},
			want: "b85d4c6f1d21",
		},
		{
			name: "replacement pseudo-version",
			buildInfo: &debug.BuildInfo{Deps: []*debug.Module{{
				Path: resourcePoolAPIModulePath,
				Replace: &debug.Module{
					Path:    resourcePoolAPIModulePath,
					Version: "v0.0.0-20260824110000-cafebabefeed",
				},
			}}},
			want: "cafebabefeed",
		},
		{
			name:      "missing dependency",
			buildInfo: &debug.BuildInfo{},
			want:      "unknown",
		},
		{
			name: "tagged dependency",
			buildInfo: &debug.BuildInfo{Deps: []*debug.Module{{
				Path:    resourcePoolAPIModulePath,
				Version: "v1.2.3",
			}}},
			want: "unknown",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := resolveCLIBuildInfo("devel", "unknown", "unknown", test.buildInfo)
			if got.ResourcePoolAPIRevision != test.want {
				t.Errorf("ResourcePoolAPIRevision = %q, want %q", got.ResourcePoolAPIRevision, test.want)
			}
			wantLine := "resource pool API: " + test.want
			if !strings.Contains(got.String(), wantLine) {
				t.Errorf("String() = %q, want line %q", got.String(), wantLine)
			}
		})
	}
}
