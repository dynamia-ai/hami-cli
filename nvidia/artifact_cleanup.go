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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	artifactSnapshotPrefix              = "hami-cli-core-"
	artifactCleanupArgument             = "--hami-cli-internal-artifact-cleanup"
	artifactCleanupFDEnvironment        = "_HAMI_CLI_ARTIFACT_CLEANUP_FD"
	artifactCleanupDirectoryEnvironment = "_HAMI_CLI_ARTIFACT_CLEANUP_DIRECTORY"
)

// RunArtifactCleanupFromEnvironment handles an explicitly requested private
// helper process. It validates the inherited cleanup state, blocks until the
// Docker client exits, then removes the Core snapshot directory. Calls without
// the private helper argument return false without inspecting the environment.
func RunArtifactCleanupFromEnvironment(arguments []string) (bool, error) {
	if len(arguments) != 1 || arguments[0] != artifactCleanupArgument {
		return false, nil
	}
	fdValue, requested := os.LookupEnv(artifactCleanupFDEnvironment)
	if !requested {
		return false, fmt.Errorf("missing Core snapshot cleanup environment %s", artifactCleanupFDEnvironment)
	}
	directory, directoryRequested := os.LookupEnv(artifactCleanupDirectoryEnvironment)
	fd, err := strconv.ParseUint(fdValue, 10, 31)
	if err != nil {
		return false, fmt.Errorf("invalid Core snapshot cleanup descriptor %q: %w", fdValue, err)
	}
	if fd != 3 {
		return false, fmt.Errorf("invalid Core snapshot cleanup descriptor %d: expected 3", fd)
	}
	if !directoryRequested || !safeArtifactSnapshotDirectory(directory) {
		return false, fmt.Errorf("invalid Core snapshot cleanup directory %q", directory)
	}
	lifetime := os.NewFile(uintptr(fd), "hami-cli-artifact-lifetime")
	if lifetime == nil {
		return false, errors.New("open Core snapshot cleanup descriptor 3")
	}
	info, err := lifetime.Stat()
	if err != nil {
		_ = lifetime.Close()
		return false, fmt.Errorf("inspect Core snapshot cleanup descriptor 3: %w", err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		_ = lifetime.Close()
		return false, fmt.Errorf("invalid Core snapshot cleanup descriptor 3: expected a pipe, got mode %s", info.Mode())
	}
	if err := validateArtifactCleanupLifetime(lifetime); err != nil {
		_ = lifetime.Close()
		return false, err
	}
	_, readErr := io.Copy(io.Discard, lifetime)
	closeErr := lifetime.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return true, fmt.Errorf("wait for Core snapshot lifetime: %w", err)
	}
	if err := os.RemoveAll(directory); err != nil {
		return true, fmt.Errorf("remove Core snapshot directory %q: %w", directory, err)
	}
	return true, nil
}

func safeArtifactSnapshotDirectory(directory string) bool {
	clean := filepath.Clean(directory)
	tempDirectory, err := filepath.Abs(os.TempDir())
	if err != nil {
		return false
	}
	return filepath.Dir(clean) == filepath.Clean(tempDirectory) &&
		strings.HasPrefix(filepath.Base(clean), artifactSnapshotPrefix)
}
