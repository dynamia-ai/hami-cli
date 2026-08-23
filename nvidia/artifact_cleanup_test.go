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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	cleanupHandled, err := RunArtifactCleanupFromEnvironment(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if cleanupHandled {
		return
	}
	os.Exit(m.Run())
}

func TestRunArtifactCleanupFromEnvironmentRejectsInvalidState(t *testing.T) {
	t.Setenv(artifactCleanupFDEnvironment, "not-a-descriptor")
	t.Setenv(artifactCleanupDirectoryEnvironment, filepath.Join(os.TempDir(), artifactSnapshotPrefix+"stale"))

	handled, err := RunArtifactCleanupFromEnvironment([]string{artifactCleanupArgument})
	if err == nil || !strings.Contains(err.Error(), "invalid Core snapshot cleanup descriptor") {
		t.Fatalf("RunArtifactCleanupFromEnvironment() error = %v, want invalid descriptor", err)
	}
	if handled {
		t.Fatal("invalid cleanup state was reported as handled")
	}
}

func TestRunArtifactCleanupFromEnvironmentRequiresExplicitHelperArgument(t *testing.T) {
	t.Setenv(artifactCleanupFDEnvironment, "not-a-descriptor")
	t.Setenv(artifactCleanupDirectoryEnvironment, filepath.Join(os.TempDir(), artifactSnapshotPrefix+"stale"))

	handled, err := RunArtifactCleanupFromEnvironment([]string{"version"})
	if err != nil {
		t.Fatalf("RunArtifactCleanupFromEnvironment() error = %v", err)
	}
	if handled {
		t.Fatal("cleanup environment without the private helper argument was handled")
	}
}

func TestRunArtifactCleanupFromEnvironmentRejectsInvalidDirectory(t *testing.T) {
	t.Setenv(artifactCleanupFDEnvironment, "3")
	t.Setenv(artifactCleanupDirectoryEnvironment, filepath.Join(os.TempDir(), "not-a-hami-snapshot"))

	handled, err := RunArtifactCleanupFromEnvironment([]string{artifactCleanupArgument})
	if err == nil || !strings.Contains(err.Error(), "invalid Core snapshot cleanup directory") {
		t.Fatalf("RunArtifactCleanupFromEnvironment() error = %v, want invalid directory", err)
	}
	if handled {
		t.Fatal("invalid cleanup directory was reported as handled")
	}
}

func TestSafeArtifactSnapshotDirectory(t *testing.T) {
	t.Parallel()
	if !safeArtifactSnapshotDirectory(filepath.Join(os.TempDir(), artifactSnapshotPrefix+"example")) {
		t.Fatal("generated snapshot directory was rejected")
	}
	for _, path := range []string{"", os.TempDir(), filepath.Join(os.TempDir(), "other"), "/var/tmp/" + artifactSnapshotPrefix + "example"} {
		if safeArtifactSnapshotDirectory(path) {
			t.Fatalf("unsafe cleanup path %q was accepted", path)
		}
	}
}
