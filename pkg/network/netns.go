// Copyright 2026 The Tardigrade Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build linux

package network

import (
	"path/filepath"
	"runtime"

	"github.com/vishvananda/netns"
)

// netnsDir must match vishvananda/netns's own (unexported) bind-mount
// path, not firecracker-go-sdk's defaultNetNSDir -- they only
// coincide via the conventional /var/run -> /run symlink.
const netnsDir = "/run/netns"

func createNetns(name string) (string, error) {
	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		h, err := netns.NewNamed(name)
		if err != nil {
			errCh <- err
			return
		}
		h.Close()
		errCh <- nil
	}()
	if err := <-errCh; err != nil {
		return "", err
	}
	return filepath.Join(netnsDir, name), nil
}

func deleteNetns(name string) error {
	return netns.DeleteNamed(name)
}
