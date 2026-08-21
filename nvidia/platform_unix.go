//go:build darwin || linux

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

	"golang.org/x/sys/unix"
)

func inspectDockerDeviceSource(source string) (bool, error) {
	var stat unix.Stat_t
	if err := unix.Stat(source, &stat); err != nil {
		return false, err
	}
	return stat.Mode&unix.S_IFMT == unix.S_IFCHR && unix.Major(uint64(stat.Rdev)) == nvidiaDeviceMajor, nil
}

func openRegularAssignmentFile(filename string) (*os.File, error) {
	fd, err := unix.Open(filename, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), filename)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open %q: invalid file descriptor", filename)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("%q is not a regular file", filename)
	}
	return file, nil
}
