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
	"os"

	"golang.org/x/sys/unix"
)

func validateArtifactCleanupLifetime(lifetime *os.File) error {
	flags, err := unix.FcntlInt(lifetime.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return fmt.Errorf("inspect Core snapshot cleanup descriptor 3 access mode: %w", err)
	}
	if flags&unix.O_ACCMODE != unix.O_RDONLY {
		return fmt.Errorf("invalid Core snapshot cleanup descriptor 3: expected a read-only pipe endpoint")
	}
	return nil
}
