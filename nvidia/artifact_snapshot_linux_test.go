//go:build linux

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
	"strings"
	"testing"
	"time"
)

func TestSystemArtifactSnapshotIsPrivateAndIndependent(t *testing.T) {
	originalPath := t.TempDir() + "/libvgpu.so"
	if err := os.WriteFile(originalPath, []byte("verified bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	snapshot, snapshotPath, cleanup, err := (systemArtifactSnapshotter{}).Snapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !strings.HasPrefix(filepath.Base(filepath.Dir(snapshotPath)), artifactSnapshotPrefix) {
		t.Fatalf("snapshot path = %q, want private snapshot directory", snapshotPath)
	}
	info, err := snapshot.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Fatalf("snapshot mode = %o, want 0400", info.Mode().Perm())
	}
	if err := os.WriteFile(originalPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "verified bytes" {
		t.Fatalf("snapshot contents = %q, want verified bytes", got)
	}
	directory := filepath.Dir(snapshotPath)
	cleanup()
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("snapshot directory still exists after cleanup: %v", err)
	}
}

func TestArtifactCleanupProcessRemovesDirectoryAfterLifetimeCloses(t *testing.T) {
	directory, err := os.MkdirTemp("", artifactSnapshotPrefix)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	lifetime, err := startArtifactCleanupProcess(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := lifetime.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err := os.Stat(directory)
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup process did not remove the snapshot directory")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
