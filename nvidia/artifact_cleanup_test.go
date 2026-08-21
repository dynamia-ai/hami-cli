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
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	if RunArtifactCleanupFromEnvironment() {
		return
	}
	os.Exit(m.Run())
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
