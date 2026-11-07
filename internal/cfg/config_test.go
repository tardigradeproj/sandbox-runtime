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

package cfg

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// defaultBoolTag is defaultTag parsed as a *bool.
func defaultBoolTag(t *testing.T, typ reflect.Type, fieldName string) *bool {
	t.Helper()
	b, err := strconv.ParseBool(defaultTag(t, typ, fieldName))
	require.NoError(t, err)
	return &b
}

func boolPtr(b bool) *bool { return &b }

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

func TestLoad(t *testing.T) {
	path := writeConfig(t, `
rootfs  = "/var/lib/sandbox-runtime/rootfs"
state = "/srv/fc"
version = "0.1.0"

[grpc]
listen_address = "unix:///var/run/sandbox-runtime.sock"

[logs]
level = "info"
format = "text"

[runtime.firecracker]
bin = "/usr/local/bin/firecracker"
kernel_image_path = "/var/lib/sandbox-runtime/vmlinux"
default_boot_args = "console=ttyS0 reboot=k panic=1 pci=off"

[runtime.firecracker.log]
level = "Info"
log_path = "/var/log/sandbox-runtime/firecracker.log"
show_level = true
show_log_origin = false
`)

	cfg, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, "/var/lib/sandbox-runtime/rootfs", cfg.Rootfs)
	assert.Equal(t, "/srv/fc", cfg.State)
	assert.Equal(t, "0.1.0", cfg.Version)
	assert.Equal(t, GRPC{
		ListenAddress: "unix:///var/run/sandbox-runtime.sock",
	}, cfg.GRPC)
	assert.Equal(t, Logs{
		Level:  "info",
		Format: "text",
	}, cfg.Logs)
	assert.Equal(t, Runtime{
		Firecracker: Firecracker{
			Bin:             "/usr/local/bin/firecracker",
			KernelImagePath: "/var/lib/sandbox-runtime/vmlinux",
			DefaultBootArgs: "console=ttyS0 reboot=k panic=1 pci=off",
			Log: FirecrackerLog{
				Level:         "Info",
				LogPath:       "/var/log/sandbox-runtime/firecracker.log",
				ShowLevel:     boolPtr(true),
				ShowLogOrigin: boolPtr(false),
			},
		},
	}, cfg.Runtime)
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, ``)

	cfg, err := Load(path)
	require.NoError(t, err)

	configType := reflect.TypeOf(Settings{})
	grpcType := reflect.TypeOf(GRPC{})
	logsType := reflect.TypeOf(Logs{})
	firecrackerType := reflect.TypeOf(Firecracker{})
	firecrackerLogType := reflect.TypeOf(FirecrackerLog{})

	assert.Equal(t, defaultTag(t, configType, "Rootfs"), cfg.Rootfs)
	assert.Equal(t, defaultTag(t, configType, "State"), cfg.State)
	assert.Equal(t, defaultTag(t, configType, "Version"), cfg.Version)
	assert.Equal(t, GRPC{
		ListenAddress: defaultTag(t, grpcType, "ListenAddress"),
	}, cfg.GRPC)
	assert.Equal(t, Logs{
		Level:  defaultTag(t, logsType, "Level"),
		Format: defaultTag(t, logsType, "Format"),
	}, cfg.Logs)
	assert.Equal(t, Runtime{
		Firecracker: Firecracker{
			Bin:             defaultTag(t, firecrackerType, "Bin"),
			KernelImagePath: defaultTag(t, firecrackerType, "KernelImagePath"),
			DefaultBootArgs: defaultTag(t, firecrackerType, "DefaultBootArgs"),
			Log: FirecrackerLog{
				Level:         defaultTag(t, firecrackerLogType, "Level"),
				LogPath:       defaultTag(t, firecrackerLogType, "LogPath"),
				ShowLevel:     defaultBoolTag(t, firecrackerLogType, "ShowLevel"),
				ShowLogOrigin: defaultBoolTag(t, firecrackerLogType, "ShowLogOrigin"),
			},
		},
	}, cfg.Runtime)
}

func TestLoadAppliesDefaultsPerField(t *testing.T) {
	path := writeConfig(t, `
rootfs = "/custom/rootfs"

[logs]
level = "debug"

[runtime.firecracker]
bin = "/custom/firecracker"

[runtime.firecracker.log]
show_level = false
`)

	cfg, err := Load(path)
	require.NoError(t, err)

	firecrackerType := reflect.TypeOf(Firecracker{})
	firecrackerLogType := reflect.TypeOf(FirecrackerLog{})

	assert.Equal(t, "/custom/rootfs", cfg.Rootfs)
	assert.Equal(t, defaultTag(t, reflect.TypeOf(Settings{}), "State"), cfg.State)
	assert.Equal(t, defaultTag(t, reflect.TypeOf(Settings{}), "Version"), cfg.Version)
	assert.Equal(t, defaultTag(t, reflect.TypeOf(GRPC{}), "ListenAddress"), cfg.GRPC.ListenAddress)
	assert.Equal(t, "debug", cfg.Logs.Level)
	assert.Equal(t, defaultTag(t, reflect.TypeOf(Logs{}), "Format"), cfg.Logs.Format)
	assert.Equal(t, "/custom/firecracker", cfg.Runtime.Firecracker.Bin)
	assert.Equal(t, defaultTag(t, firecrackerType, "KernelImagePath"), cfg.Runtime.Firecracker.KernelImagePath)
	assert.Equal(t, defaultTag(t, firecrackerType, "DefaultBootArgs"), cfg.Runtime.Firecracker.DefaultBootArgs)
	assert.Equal(t, defaultTag(t, firecrackerLogType, "Level"), cfg.Runtime.Firecracker.Log.Level)
	assert.Equal(t, defaultTag(t, firecrackerLogType, "LogPath"), cfg.Runtime.Firecracker.Log.LogPath)
	require.NotNil(t, cfg.Runtime.Firecracker.Log.ShowLevel)
	assert.False(t, *cfg.Runtime.Firecracker.Log.ShowLevel)
	assert.Equal(t, defaultBoolTag(t, firecrackerLogType, "ShowLogOrigin"), cfg.Runtime.Firecracker.Log.ShowLogOrigin)
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
