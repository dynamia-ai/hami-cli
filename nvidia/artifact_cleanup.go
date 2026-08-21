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
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	artifactSnapshotPrefix              = "hami-cli-core-"
	artifactCleanupFDEnvironment        = "_HAMI_CLI_ARTIFACT_CLEANUP_FD"
	artifactCleanupDirectoryEnvironment = "_HAMI_CLI_ARTIFACT_CLEANUP_DIRECTORY"
)

// RunArtifactCleanupFromEnvironment blocks a private helper process until the
// Docker client exits, then removes the Core snapshot directory.
func RunArtifactCleanupFromEnvironment() bool {
	fdValue, requested := os.LookupEnv(artifactCleanupFDEnvironment)
	if !requested {
		return false
	}
	directory := os.Getenv(artifactCleanupDirectoryEnvironment)
	fd, err := strconv.ParseUint(fdValue, 10, 31)
	if err != nil || fd != 3 || !safeArtifactSnapshotDirectory(directory) {
		return true
	}
	lifetime := os.NewFile(uintptr(fd), "hami-cli-artifact-lifetime")
	if lifetime == nil {
		return true
	}
	_, _ = io.Copy(io.Discard, lifetime)
	_ = lifetime.Close()
	_ = os.RemoveAll(directory)
	return true
}

func safeArtifactSnapshotDirectory(directory string) bool {
	clean := filepath.Clean(directory)
	return filepath.Dir(clean) == filepath.Clean(os.TempDir()) &&
		strings.HasPrefix(filepath.Base(clean), artifactSnapshotPrefix)
}
