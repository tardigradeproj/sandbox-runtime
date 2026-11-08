package cfg

import (
	"fmt"
	"os"
	"reflect"
	"strconv"

	"github.com/pelletier/go-toml/v2"
)

type Settings struct {
	Rootfs Rootfs `toml:"rootfs"`
	// State is the directory under which per-sandbox microVM state (API
	// sockets, overlay images, etc.) is placed.
	State string `toml:"state" default:"/run/sandbox-runtime"`
	// Version populates VersionResponse.
	Version string  `toml:"version" default:"0.1.0"`
	GRPC    GRPC    `toml:"grpc"`
	Logs    Logs    `toml:"logs"`
	Runtime Runtime `toml:"runtime"`
}

// Rootfs holds settings for the local rootfs image cache.
type Rootfs struct {
	// Dir is the directory under which pulled rootfs images are cached.
	Dir string `toml:"dir" default:"/var/lib/sandbox-runtime/rootfs"`
	// Packer selects how PullRootFS packs a pulled rootfs into an
	// image.
	Packer Packer `toml:"packer"`
}

// Packer configures the filesystem format PullRootFS packs rootfs
// images into.
type Packer struct {
	// Type is "erofs" or "ext4".
	Type string `toml:"type" default:"erofs"`
}

// GRPC holds settings for the gRPC server.
type GRPC struct {
	// ListenAddress is where RuntimeService/RootFSService's gRPC server
	// binds, e.g. "unix:///var/run/sandbox-runtime.sock" or
	// "127.0.0.1:9090".
	ListenAddress string `toml:"listen_address" default:"unix:///var/run/sandbox-runtime.sock"`
}

// Logs holds settings for the runtime daemon's own logging.
type Logs struct {
	Level string `toml:"level" default:"info"`
	// Format is the log output format, "text" or "json".
	Format string `toml:"format" default:"text"`
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
	KernelImagePath string         `toml:"kernel_image_path" default:"/var/lib/sandbox-runtime/vmlinux"`
	DefaultBootArgs string         `toml:"default_boot_args" default:"console=ttyS0 reboot=k panic=1 pci=off"`
	Log             FirecrackerLog `toml:"log"`
}

// FirecrackerLog configures firecracker's own logger. LogPath isn't
// configurable here: each sandbox's log lives within its own state
// dir (see CreateSandbox), not a shared path.
type FirecrackerLog struct {
	Level         string `toml:"level" default:"Info"`
	ShowLevel     *bool  `toml:"show_level" default:"true"`
	ShowLogOrigin *bool  `toml:"show_log_origin" default:"false"`
}

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
		case reflect.Pointer:
			if field.IsNil() && field.Type().Elem().Kind() == reflect.Bool {
				if def, ok := t.Field(i).Tag.Lookup("default"); ok {
					if b, err := strconv.ParseBool(def); err == nil {
						field.Set(reflect.ValueOf(&b))
					}
				}
			}
		}
	}
}

// Load reads and parses the TOML config file at path.
func Load(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var cfg Settings
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	applyDefaults(reflect.ValueOf(&cfg).Elem())
	return &cfg, nil
}
