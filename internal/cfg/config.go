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
	Jailer          Jailer         `toml:"jailer"`
}

// Jailer configures running Firecracker under the jailer binary.
type Jailer struct {
	Enabled bool `toml:"enabled" default:"false"`
	// BinPath is resolved via PATH if it contains no path separators.
	BinPath string `toml:"bin_path" default:"jailer"`
	// ChrootBaseDir must be on the same filesystem as the rootfs
	// store and overlay images: jailer hard-links them into the chroot.
	ChrootBaseDir string `toml:"chroot_base_dir" default:"/run/sandbox-runtime/jailer"`
	UIDGID        UIDGID `toml:"uid_gid"`
}

// UIDGID controls how the uid/gid jailer drops privileges to is
// obtained for each sandbox: Mode is "precreated" (reuse
// StaticUID/StaticGID) or "on_demand" (allocate from
// [RangeStart, RangeEnd], released on teardown).
type UIDGID struct {
	Mode string `toml:"mode" default:"precreated"`
	// No default: must be set explicitly when Mode == "precreated".
	StaticUID *int `toml:"static_uid"`
	StaticGID *int `toml:"static_gid"`
	// Used when Mode == "on_demand".
	RangeStart *int `toml:"range_start"`
	RangeEnd   *int `toml:"range_end"`
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
			if !field.IsNil() {
				continue
			}
			def, ok := t.Field(i).Tag.Lookup("default")
			if !ok {
				continue
			}
			switch field.Type().Elem().Kind() {
			case reflect.Bool:
				if b, err := strconv.ParseBool(def); err == nil {
					field.Set(reflect.ValueOf(&b))
				}
			case reflect.Int:
				if n, err := strconv.Atoi(def); err == nil {
					field.Set(reflect.ValueOf(&n))
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
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return &cfg, nil
}

// validate checks invariants applyDefaults can't express via a plain
// "default" tag.
func validate(cfg *Settings) error {
	j := cfg.Runtime.Firecracker.Jailer
	if !j.Enabled {
		return nil
	}
	switch j.UIDGID.Mode {
	case "precreated":
		if j.UIDGID.StaticUID == nil || j.UIDGID.StaticGID == nil {
			return fmt.Errorf("runtime.firecracker.jailer.uid_gid: static_uid and static_gid must be set when mode is %q", j.UIDGID.Mode)
		}
	case "on_demand":
		if j.UIDGID.RangeStart == nil || j.UIDGID.RangeEnd == nil {
			return fmt.Errorf("runtime.firecracker.jailer.uid_gid: range_start and range_end must be set when mode is %q", j.UIDGID.Mode)
		}
		if *j.UIDGID.RangeStart > *j.UIDGID.RangeEnd {
			return fmt.Errorf("runtime.firecracker.jailer.uid_gid: range_start %d must be <= range_end %d", *j.UIDGID.RangeStart, *j.UIDGID.RangeEnd)
		}
	default:
		return fmt.Errorf("runtime.firecracker.jailer.uid_gid: mode must be %q or %q, got %q", "precreated", "on_demand", j.UIDGID.Mode)
	}
	return nil
}
