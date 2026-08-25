package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func parseCLIBuildInfoOutput(t *testing.T, output string) map[string]string {
	t.Helper()
	fields := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			t.Fatalf("version output line %q has no separator", line)
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return fields
}

func TestResolveCLIBuildInfo(t *testing.T) {
	t.Parallel()
	pseudoVersion := "v0.0.0-20260817092328-9214a562258d"
	tests := []struct {
		name        string
		version     string
		revision    string
		buildDate   string
		mainVersion string
		settings    []debug.BuildSetting
		want        cliBuildInfo
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
		{
			name:        "tagged module version",
			version:     "devel",
			revision:    "unknown",
			buildDate:   "unknown",
			mainVersion: "v1.2.3",
			want:        cliBuildInfo{Version: "v1.2.3", Revision: "unknown", CommitDate: "unknown", BuildDate: "unknown"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mainVersion := test.mainVersion
			if mainVersion == "" {
				mainVersion = pseudoVersion
			}
			got := resolveCLIBuildInfo(test.version, test.revision, test.buildDate, &debug.BuildInfo{
				Main:     debug.Module{Version: mainVersion},
				Settings: test.settings,
			})
			if got.Version != test.want.Version || got.Revision != test.want.Revision || got.CommitDate != test.want.CommitDate || got.BuildDate != test.want.BuildDate {
				t.Fatalf("resolveCLIBuildInfo() = %#v, want version metadata %#v", got, test.want)
			}
			output := parseCLIBuildInfoOutput(t, got.String())
			for field, want := range map[string]string{
				"version":     test.want.Version,
				"revision":    test.want.Revision,
				"commit date": test.want.CommitDate,
				"build date":  test.want.BuildDate,
			} {
				if output[field] != want {
					t.Errorf("version output %q = %q, want %q", field, output[field], want)
				}
			}
		})
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
			output := parseCLIBuildInfoOutput(t, got.String())
			if output["resource pool API"] != test.want {
				t.Errorf("version output resource pool API = %q, want %q", output["resource pool API"], test.want)
			}
		})
	}
}
