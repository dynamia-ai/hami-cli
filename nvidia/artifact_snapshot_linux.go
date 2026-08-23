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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

func (snapshotter systemArtifactSnapshotter) Snapshot(source *os.File) (*os.File, string, func(), error) {
	directory, err := os.MkdirTemp("", artifactSnapshotPrefix)
	if err != nil {
		return nil, "", nil, err
	}
	originalDirectory := directory
	directory, err = filepath.Abs(directory)
	if err != nil {
		_ = os.RemoveAll(originalDirectory)
		return nil, "", nil, err
	}
	fail := func(err error) (*os.File, string, func(), error) {
		_ = os.RemoveAll(directory)
		return nil, "", nil, err
	}
	filesystemFlags := systemSnapshotFilesystemFlags
	if snapshotter.filesystemFlags != nil {
		filesystemFlags = snapshotter.filesystemFlags
	}
	flags, err := filesystemFlags(directory)
	if err != nil {
		return fail(fmt.Errorf("inspect Core snapshot filesystem: %w", err))
	}
	if flags&uint64(unix.ST_NOEXEC) != 0 {
		return fail(fmt.Errorf("core snapshot filesystem for %q is mounted noexec; set TMPDIR to an exec-capable local filesystem", directory))
	}

	snapshotPath := filepath.Join(directory, "libvgpu.so")
	snapshot, err := os.OpenFile(snapshotPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return fail(err)
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		_ = snapshot.Close()
		return fail(err)
	}
	if _, err := io.Copy(snapshot, source); err != nil {
		_ = snapshot.Close()
		return fail(err)
	}
	if err := snapshot.Sync(); err != nil {
		_ = snapshot.Close()
		return fail(err)
	}
	if err := snapshot.Chmod(0o444); err != nil {
		_ = snapshot.Close()
		return fail(err)
	}
	if err := snapshot.Close(); err != nil {
		return fail(err)
	}
	snapshot, err = os.Open(snapshotPath)
	if err != nil {
		return fail(err)
	}

	lifetime, err := startArtifactCleanupProcess(directory)
	if err != nil {
		_ = snapshot.Close()
		return fail(err)
	}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_ = snapshot.Close()
			_ = lifetime.Close()
			_ = os.RemoveAll(directory)
		})
	}
	return snapshot, snapshotPath, cleanup, nil
}

func systemSnapshotFilesystemFlags(path string) (uint64, error) {
	var filesystem unix.Statfs_t
	if err := unix.Statfs(path, &filesystem); err != nil {
		return 0, err
	}
	return uint64(filesystem.Flags), nil
}

func startArtifactCleanupProcess(directory string) (*os.File, error) {
	readLifetime, writeLifetime, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) {
		_ = readLifetime.Close()
		_ = writeLifetime.Close()
		return nil, err
	}
	flags, err := unix.FcntlInt(writeLifetime.Fd(), unix.F_GETFD, 0)
	if err != nil {
		return fail(err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	command := exec.Command(executable, artifactCleanupArgument)
	command.Env = append(os.Environ(),
		artifactCleanupFDEnvironment+"=3",
		artifactCleanupDirectoryEnvironment+"="+directory,
	)
	command.ExtraFiles = []*os.File{readLifetime}
	if err := command.Start(); err != nil {
		return fail(fmt.Errorf("start Core snapshot cleanup process: %w", err))
	}
	_ = readLifetime.Close()
	if _, err := unix.FcntlInt(writeLifetime.Fd(), unix.F_SETFD, flags&^unix.FD_CLOEXEC); err != nil {
		return fail(err)
	}
	return writeLifetime, nil
}
