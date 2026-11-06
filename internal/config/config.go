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

// Package config loads the runtime's on-disk TOML configuration, e.g.:
//
//	rootfs  = "/var/lib/sandbox-runtime/rootfs"
//	version = "0.1.0"
//
//	[grpc]
//	listen_address = "unix:///var/run/sandbox-runtime.sock"
//
//	[runtime.firecracker]
//	bin               = "/usr/local/bin/firecracker"
//	kernel_image_path = "/var/lib/sandbox-runtime/vmlinux"
//	default_boot_args = "console=ttyS0 reboot=k panic=1 pci=off"
//	api_sock_dir      = "/var/run/sandbox-runtime/firecracker"
//
// Any property left unset falls back to the value in its `default` struct
// tag.
package config

import (
	"fmt"
	"os"
	"reflect"

	"github.com/pelletier/go-toml/v2"
)

// Config is the sandbox-runtime's application configuration.
type Config struct {
	// Rootfs is the directory under which pulled rootfs images are cached
	Rootfs string `toml:"rootfs" default:"/var/lib/sandbox-runtime/rootfs"`
	// Version populates VersionResponse.
	Version string  `toml:"version" default:"0.1.0"`
	GRPC    GRPC    `toml:"grpc"`
	Runtime Runtime `toml:"runtime"`
}

// GRPC holds settings for the gRPC server.
type GRPC struct {
	// ListenAddress is where RuntimeService/RootFSService's gRPC server
	// binds, e.g. "unix:///var/run/sandbox-runtime.sock" or
	// "127.0.0.1:9090".
	ListenAddress string `toml:"listen_address" default:"unix:///var/run/sandbox-runtime.sock"`
}

// Runtime holds settings for the runtime daemon itself.
type Runtime struct {
	Firecracker Firecracker `toml:"firecracker"`
}

// Firecracker holds settings needed to actually launch a Firecracker VM.
type Firecracker struct {
	// Bin is the path to the firecracker binary.
	Bin string `toml:"bin" default:"/usr/local/bin/firecracker"`
	// KernelImagePath and BootArgs are defaults for BootConfig when a
	// sandbox request doesn't override them.
	KernelImagePath string `toml:"kernel_image_path" default:"/var/lib/sandbox-runtime/vmlinux"`
	DefaultBootArgs string `toml:"default_boot_args" default:"console=ttyS0 reboot=k panic=1 pci=off"`

	// APISockDir is the directory firecracker's --api-socket files are
	// created in; each sandbox gets its own socket within this directory.
	APISockDir string `toml:"api_sock_dir" default:"/var/run/sandbox-runtime/firecracker"`
}

// applyDefaults walks v and, for every empty string field, fills in the
// value from its `default` struct tag. It recurses into nested structs.
func applyDefaults(v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := v.Field(i)
		switch field.Kind() {
		case reflect.Struct:
			applyDefaults(field)
		case reflect.String:
			if field.String() == "" {
				if def, ok := t.Field(i).Tag.Lookup("default"); ok {
					field.SetString(def)
				}
			}
		}
	}
}

// Load reads and parses the TOML config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	applyDefaults(reflect.ValueOf(&cfg).Elem())
	return &cfg, nil
}
