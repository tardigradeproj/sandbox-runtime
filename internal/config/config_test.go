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

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultTag returns the `default` struct tag of fieldName on typ, so
// tests assert against the tag itself rather than a duplicated literal.
func defaultTag(t *testing.T, typ reflect.Type, fieldName string) string {
	t.Helper()
	f, ok := typ.FieldByName(fieldName)
	require.True(t, ok, "field %s not found on %s", fieldName, typ)
	def, ok := f.Tag.Lookup("default")
	require.True(t, ok, "field %s on %s has no default tag", fieldName, typ)
	return def
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

func TestLoad(t *testing.T) {
	path := writeConfig(t, `
rootfs  = "/var/lib/sandbox-runtime/rootfs"
version = "0.1.0"

[grpc]
listen_address = "unix:///var/run/sandbox-runtime.sock"

[runtime.firecracker]
bin = "/usr/local/bin/firecracker"
kernel_image_path = "/var/lib/sandbox-runtime/vmlinux"
default_boot_args = "console=ttyS0 reboot=k panic=1 pci=off"
api_sock_dir = "/var/run/sandbox-runtime/firecracker"
`)

	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, "/var/lib/sandbox-runtime/rootfs", cfg.Rootfs)
	assert.Equal(t, "0.1.0", cfg.Version)
	assert.Equal(t, GRPC{
		ListenAddress: "unix:///var/run/sandbox-runtime.sock",
	}, cfg.GRPC)
	assert.Equal(t, Runtime{
		Firecracker: Firecracker{
			Bin:             "/usr/local/bin/firecracker",
			KernelImagePath: "/var/lib/sandbox-runtime/vmlinux",
			DefaultBootArgs: "console=ttyS0 reboot=k panic=1 pci=off",
			APISockDir:      "/var/run/sandbox-runtime/firecracker",
		},
	}, cfg.Runtime)
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, ``)

	cfg, err := Load(path)
	require.NoError(t, err)

	configType := reflect.TypeOf(Config{})
	grpcType := reflect.TypeOf(GRPC{})
	firecrackerType := reflect.TypeOf(Firecracker{})

	assert.Equal(t, defaultTag(t, configType, "Rootfs"), cfg.Rootfs)
	assert.Equal(t, defaultTag(t, configType, "Version"), cfg.Version)
	assert.Equal(t, GRPC{
		ListenAddress: defaultTag(t, grpcType, "ListenAddress"),
	}, cfg.GRPC)
	assert.Equal(t, Runtime{
		Firecracker: Firecracker{
			Bin:             defaultTag(t, firecrackerType, "Bin"),
			KernelImagePath: defaultTag(t, firecrackerType, "KernelImagePath"),
			DefaultBootArgs: defaultTag(t, firecrackerType, "DefaultBootArgs"),
			APISockDir:      defaultTag(t, firecrackerType, "APISockDir"),
		},
	}, cfg.Runtime)
}

func TestLoadAppliesDefaultsPerField(t *testing.T) {
	path := writeConfig(t, `
rootfs = "/custom/rootfs"

[runtime.firecracker]
bin = "/custom/firecracker"
`)

	cfg, err := Load(path)
	require.NoError(t, err)

	firecrackerType := reflect.TypeOf(Firecracker{})

	assert.Equal(t, "/custom/rootfs", cfg.Rootfs)
	assert.Equal(t, defaultTag(t, reflect.TypeOf(Config{}), "Version"), cfg.Version)
	assert.Equal(t, defaultTag(t, reflect.TypeOf(GRPC{}), "ListenAddress"), cfg.GRPC.ListenAddress)
	assert.Equal(t, "/custom/firecracker", cfg.Runtime.Firecracker.Bin)
	assert.Equal(t, defaultTag(t, firecrackerType, "KernelImagePath"), cfg.Runtime.Firecracker.KernelImagePath)
	assert.Equal(t, defaultTag(t, firecrackerType, "DefaultBootArgs"), cfg.Runtime.Firecracker.DefaultBootArgs)
	assert.Equal(t, defaultTag(t, firecrackerType, "APISockDir"), cfg.Runtime.Firecracker.APISockDir)
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	assert.Error(t, err)
}

func TestLoadInvalidTOML(t *testing.T) {
	path := writeConfig(t, `this is not valid toml =`)
	_, err := Load(path)
	assert.Error(t, err)
}
