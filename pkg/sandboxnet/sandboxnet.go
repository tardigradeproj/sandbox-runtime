//go:build linux

// Package sandboxnet creates per-sandbox network namespaces.
package sandboxnet

import (
	"path/filepath"
	"runtime"

	"github.com/vishvananda/netns"
)

// Dir must match vishvananda/netns's own (unexported) bind-mount
// path, not firecracker-go-sdk's unrelated defaultNetNSDir constant --
// they only coincide via the conventional /var/run -> /run symlink.
const Dir = "/run/netns"

func Create(name string) (string, error) {
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
	return filepath.Join(Dir, name), nil
}

// Delete removes the network namespace named name.
func Delete(name string) error {
	return netns.DeleteNamed(name)
}
