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

package runtime

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tardigradeproj/sandbox-runtime/internal/cfg"
	v1 "github.com/tardigradeproj/sandbox-runtime/pkg/api/runtime/v1"
	"github.com/tardigradeproj/sandbox-runtime/pkg/filesystem/store"
	"github.com/tardigradeproj/sandbox-runtime/pkg/sandboxnet"
)

// fakeFirecrackerEnv marks a process as the fake firecracker binary
// (see TestMain), rather than the test binary running its usual suite.
// fakeFirecrackerFailPutEnv, when also set, makes it fail every PUT
// request, so CreateSandbox's cleanup path can be exercised after the
// process has already been spawned and partially configured.
const (
	fakeFirecrackerEnv        = "GO_WANT_FAKE_FIRECRACKER"
	fakeFirecrackerFailPutEnv = "GO_WANT_FAKE_FIRECRACKER_FAIL_PUT"
	// fakeFirecrackerReqLogEnv, when set to a file path, makes the fake
	// process append "<method> <path>" for every request it receives,
	// so tests can assert on what was actually sent (e.g. that
	// StartSandbox really issued a PUT /actions).
	fakeFirecrackerReqLogEnv = "GO_WANT_FAKE_FIRECRACKER_REQ_LOG"
	// fakeFirecrackerPidFileEnv, when set to a file path, makes the
	// fake process write its own pid there once it's listening. Tests
	// whose sandbox is meant to keep running (CreateSandbox/StartSandbox
	// never kill it, by design) use this to reap the process in
	// t.Cleanup -- otherwise it inherits this test binary's own
	// stdout/stderr (see runtime_linux.go) and go test hangs waiting
	// for that pipe to close.
	fakeFirecrackerPidFileEnv = "GO_WANT_FAKE_FIRECRACKER_PID_FILE"
)

// TestMain lets this test binary re-exec itself as a stand-in for the
// firecracker binary: CreateSandbox really spawns a process and talks
// to it over its --api-sock, so tests point Firecracker.Bin at
// os.Args[0] with fakeFirecrackerEnv set, and intercept here before
// the usual testing flag parsing (which doesn't know firecracker's own
// flags) ever sees argv.
func TestMain(m *testing.M) {
	if os.Getenv(fakeFirecrackerEnv) == "1" {
		runFakeFirecracker(os.Getenv(fakeFirecrackerFailPutEnv) == "1")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runFakeFirecracker stands in for the real firecracker binary: it
// listens on the --api-sock path from argv and answers every request
// with 204, the success code every config PUT endpoint we hit
// (machine-config, boot-source, drives, logger) expects. If failPuts
// is set, every PUT instead fails, simulating a mid-configuration
// error after the process is already up.
func runFakeFirecracker(failPuts bool) {
	var sockPath string
	for i, a := range os.Args {
		if a == "--api-sock" && i+1 < len(os.Args) {
			sockPath = os.Args[i+1]
		}
	}
	if sockPath == "" {
		fmt.Fprintln(os.Stderr, "fake firecracker: missing --api-sock")
		return
	}

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake firecracker: listen:", err)
		return
	}
	defer l.Close()

	if pidFile := os.Getenv(fakeFirecrackerPidFileEnv); pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0o644)
	}

	reqLogPath := os.Getenv(fakeFirecrackerReqLogEnv)

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reqLogPath != "" {
			if f, err := os.OpenFile(reqLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				fmt.Fprintf(f, "%s %s\n", r.Method, r.URL.Path)
				f.Close()
			}
		}
		if r.Method == http.MethodGet {
			// waitForSocket polls GET /machine-config as a readiness
			// probe; it needs a 200 with a body matching
			// models.MachineConfiguration, not just any 2xx.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"vcpu_count":1,"mem_size_mib":128}`))
			return
		}
		if failPuts {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	go srv.Serve(l)

	// Bounded lifetime so this never lingers as an orphaned process
	// past the test that spawned it.
	time.Sleep(10 * time.Second)
}

// fakeMkfsExt4 points mkfsExt4Bin at a script that just records its
// args and exits 0, so tests don't need a real mkfs.ext4 binary.
func fakeMkfsExt4(t *testing.T, argsFile string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-mkfs.ext4")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shQuote(argsFile) + "\nexit 0\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	old := mkfsExt4Bin
	mkfsExt4Bin = script
	t.Cleanup(func() { mkfsExt4Bin = old })
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// seedRootfsEntry commits a placeholder entry to st so Resolve(image)
// succeeds, without needing a real pulled rootfs image.
func seedRootfsEntry(t *testing.T, st *store.Store, image string) {
	t.Helper()
	_, tmpImage, cleanup, err := st.Stage()
	require.NoError(t, err)
	defer cleanup()
	require.NoError(t, os.WriteFile(tmpImage, []byte("fake erofs image"), 0o644))

	require.NoError(t, st.Commit(store.Entry{
		Digest:    "sha256:deadbeef",
		RepoTags:  []string{image},
		SpecImage: image,
	}, tmpImage))
}

func newTestRuntime(t *testing.T, state string) (*Runtime, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	require.NoError(t, err)
	r, err := NewRuntime(&cfg.Settings{State: state}, st)
	require.NoError(t, err)
	return r, st
}

// newTestRuntimeWithFakeFirecracker is newTestRuntime, but Firecracker.Bin
// points at this test binary re-exec'd as the fake firecracker process
// (see TestMain), so CreateSandbox's real spawn-and-configure flow has
// something live to talk to. If failPuts is set, the fake process fails
// every config PUT, simulating a failure after it's already running.
func newTestRuntimeWithFakeFirecracker(t *testing.T, state string, failPuts bool) (*Runtime, *store.Store) {
	t.Helper()
	require.NoError(t, os.Setenv(fakeFirecrackerEnv, "1"))
	t.Cleanup(func() { os.Unsetenv(fakeFirecrackerEnv) })
	if failPuts {
		require.NoError(t, os.Setenv(fakeFirecrackerFailPutEnv, "1"))
		t.Cleanup(func() { os.Unsetenv(fakeFirecrackerFailPutEnv) })
	}

	// CreateSandbox/StartSandbox deliberately leave the fake process
	// running on success (that's the point). It now inherits this test
	// binary's own stdout/stderr (see runtime_linux.go), so it must be
	// reaped before this test returns or go test hangs waiting for that
	// pipe to close.
	pidFile := filepath.Join(t.TempDir(), "firecracker.pid")
	require.NoError(t, os.Setenv(fakeFirecrackerPidFileEnv, pidFile))
	t.Cleanup(func() {
		os.Unsetenv(fakeFirecrackerPidFileEnv)
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			return
		}
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
		}
	})

	// firecracker.Config.Validate (run as part of Handlers.Run) stats
	// the kernel image path, so it must exist even though nothing in
	// this test actually boots a kernel.
	kernelImagePath := filepath.Join(t.TempDir(), "vmlinux")
	require.NoError(t, os.WriteFile(kernelImagePath, []byte("fake kernel"), 0o644))

	st, err := store.Open(t.TempDir())
	require.NoError(t, err)
	r, err := NewRuntime(&cfg.Settings{
		State: state,
		Runtime: cfg.Runtime{
			Firecracker: cfg.Firecracker{
				Bin:             os.Args[0],
				KernelImagePath: kernelImagePath,
			},
		},
	}, st)
	require.NoError(t, err)
	return r, st
}

func TestCreateSandboxRequiresUid(t *testing.T) {
	r, _ := newTestRuntime(t, t.TempDir())
	_, err := r.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
		Config: &v1.SandboxConfig{Resources: &v1.SandboxResources{StorageBytes: 1024}},
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateSandboxRequiresStorageBytes(t *testing.T) {
	r, _ := newTestRuntime(t, t.TempDir())
	_, err := r.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
		Config: &v1.SandboxConfig{
			Metadata:  &v1.SandboxMetadata{Uid: "sandbox-1"},
			Resources: &v1.SandboxResources{MemoryBytes: 128 << 20},
		},
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateSandboxRequiresMemoryBytes(t *testing.T) {
	r, _ := newTestRuntime(t, t.TempDir())
	_, err := r.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
		Config: &v1.SandboxConfig{
			Metadata:  &v1.SandboxMetadata{Uid: "sandbox-1"},
			Resources: &v1.SandboxResources{StorageBytes: 1024},
		},
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateSandboxRequiresImage(t *testing.T) {
	r, _ := newTestRuntime(t, t.TempDir())
	_, err := r.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
		Config: &v1.SandboxConfig{
			Metadata:  &v1.SandboxMetadata{Uid: "sandbox-1"},
			Resources: &v1.SandboxResources{StorageBytes: 1024, MemoryBytes: 128 << 20},
		},
	})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateSandboxUnknownImage(t *testing.T) {
	r, _ := newTestRuntime(t, t.TempDir())
	_, err := r.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
		Config: &v1.SandboxConfig{
			Metadata:  &v1.SandboxMetadata{Uid: "sandbox-1"},
			Resources: &v1.SandboxResources{StorageBytes: 1024, MemoryBytes: 128 << 20},
			Rootfs:    &v1.RootFSMount{Spec: &v1.RootFSSpec{Image: "does-not-exist:latest"}},
		},
	})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestCreateSandboxCreatesOverlayImage(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	fakeMkfsExt4(t, argsFile)

	stateDir := t.TempDir()
	r, st := newTestRuntimeWithFakeFirecracker(t, stateDir, false)
	seedRootfsEntry(t, st, "myimage:latest")

	resp, err := r.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
		Config: &v1.SandboxConfig{
			Metadata:  &v1.SandboxMetadata{Uid: "sandbox-1"},
			Resources: &v1.SandboxResources{StorageBytes: 4 << 30, MemoryBytes: 128 << 20},
			Rootfs:    &v1.RootFSMount{Spec: &v1.RootFSSpec{Image: "myimage:latest"}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "sandbox-1", resp.GetSandboxId())

	overlayPath := filepath.Join(stateDir, resp.GetSandboxId(), "overlay.ext4")
	fi, err := os.Stat(overlayPath)
	require.NoError(t, err)
	assert.EqualValues(t, 4<<30, fi.Size())

	got, err := os.ReadFile(argsFile)
	require.NoError(t, err)
	args := strings.Fields(string(got))
	assert.Equal(t, []string{"-q", "-m", "0", overlayPath}, args)

	socketPath := filepath.Join(stateDir, "sandbox-1", "firecracker.sock")
	_, err = os.Stat(socketPath)
	assert.NoError(t, err, "expected firecracker's api socket to exist")
}

func TestCreateSandboxMkfsFailureSurfacesError(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-mkfs.ext4")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\necho 'mkfs.ext4: boom' >&2\nexit 1\n"), 0o755))
	old := mkfsExt4Bin
	mkfsExt4Bin = script
	t.Cleanup(func() { mkfsExt4Bin = old })

	stateDir := t.TempDir()
	r, st := newTestRuntime(t, stateDir)
	seedRootfsEntry(t, st, "myimage:latest")

	_, err := r.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
		Config: &v1.SandboxConfig{
			Metadata:  &v1.SandboxMetadata{Uid: "sandbox-1"},
			Resources: &v1.SandboxResources{StorageBytes: 1024, MemoryBytes: 128 << 20},
			Rootfs:    &v1.RootFSMount{Spec: &v1.RootFSSpec{Image: "myimage:latest"}},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Contains(t, err.Error(), "boom")

	_, err = os.Stat(filepath.Join(stateDir, "sandbox-1"))
	assert.True(t, os.IsNotExist(err), "expected sandbox dir to be cleaned up after mkfs failure")
}

func TestCreateSandboxCleansUpAfterFirecrackerConfigFailure(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	fakeMkfsExt4(t, argsFile)

	stateDir := t.TempDir()
	r, st := newTestRuntimeWithFakeFirecracker(t, stateDir, true /* failPuts */)
	seedRootfsEntry(t, st, "myimage:latest")

	_, err := r.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
		Config: &v1.SandboxConfig{
			Metadata:  &v1.SandboxMetadata{Uid: "sandbox-1"},
			Resources: &v1.SandboxResources{StorageBytes: 4 << 30, MemoryBytes: 128 << 20},
			Rootfs:    &v1.RootFSMount{Spec: &v1.RootFSSpec{Image: "myimage:latest"}},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))

	// The firecracker process was already spawned (and configured the
	// socket) by the time the PUT failed; cleanup must still remove the
	// whole sandbox dir, not just leave the half-configured mess behind.
	_, err = os.Stat(filepath.Join(stateDir, "sandbox-1"))
	assert.True(t, os.IsNotExist(err), "expected sandbox dir to be cleaned up after a firecracker config failure")
}

// requireNetNSSupport skips the test if the environment can't create
// network namespaces (needs CAP_SYS_ADMIN, same as jailer itself).
func requireNetNSSupport(t *testing.T) {
	t.Helper()
	const probe = "sandbox-runtime-netns-probe"
	if _, err := sandboxnet.Create(probe); err != nil {
		t.Skipf("network namespaces unavailable: %v", err)
	}
	require.NoError(t, sandboxnet.Delete(probe))
}

func TestCreateSandboxJailerEnabledAttemptsJailerSpawn(t *testing.T) {
	requireNetNSSupport(t)

	argsFile := filepath.Join(t.TempDir(), "args.txt")
	fakeMkfsExt4(t, argsFile)

	stateDir := t.TempDir()
	kernelImagePath := filepath.Join(t.TempDir(), "vmlinux")
	require.NoError(t, os.WriteFile(kernelImagePath, []byte("fake kernel"), 0o644))

	staticUID, staticGID := 1000, 1000
	st, err := store.Open(t.TempDir())
	require.NoError(t, err)
	r, err := NewRuntime(&cfg.Settings{
		State: stateDir,
		Runtime: cfg.Runtime{
			Firecracker: cfg.Firecracker{
				Bin:             "/usr/local/bin/firecracker",
				KernelImagePath: kernelImagePath,
				Jailer: cfg.Jailer{
					Enabled: true,
					// Not a real binary: just confirms CreateSandbox
					// actually attempts jailer rather than falling back.
					BinPath:       "definitely-not-a-real-jailer-binary",
					ChrootBaseDir: t.TempDir(),
					UIDGID:        cfg.UIDGID{Mode: "precreated", StaticUID: &staticUID, StaticGID: &staticGID},
				},
			},
		},
		Network: cfg.Network{Cidr: "10.201.5.0/24", BridgeName: "sri-test0"},
	}, st)
	require.NoError(t, err)
	seedRootfsEntry(t, st, "myimage:latest")

	_, err = r.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
		Config: &v1.SandboxConfig{
			Metadata:  &v1.SandboxMetadata{Uid: "sandbox-1"},
			Resources: &v1.SandboxResources{StorageBytes: 1024, MemoryBytes: 128 << 20},
			Rootfs:    &v1.RootFSMount{Spec: &v1.RootFSSpec{Image: "myimage:latest"}},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	// Confirms the failure is from the spawn attempt, i.e. chown ran first and succeeded.
	assert.Contains(t, err.Error(), "definitely-not-a-real-jailer-binary")

	_, err = os.Stat(filepath.Join(stateDir, "sandbox-1"))
	assert.True(t, os.IsNotExist(err), "expected sandbox dir to be cleaned up after a jailer spawn failure")
}

func TestBuildJailerConfig(t *testing.T) {
	fc := cfg.Firecracker{
		Bin: "/usr/local/bin/firecracker",
		Jailer: cfg.Jailer{
			BinPath:       "/usr/bin/jailer",
			ChrootBaseDir: "/run/sandbox-runtime/jailer",
		},
	}

	jc := buildJailerConfig(fc, 123, 456, "sandbox-1", "/var/lib/sandbox-runtime/vmlinux")

	require.NotNil(t, jc)
	assert.Equal(t, "sandbox-1", jc.ID)
	require.NotNil(t, jc.UID)
	assert.Equal(t, 123, *jc.UID)
	require.NotNil(t, jc.GID)
	assert.Equal(t, 456, *jc.GID)
	require.NotNil(t, jc.NumaNode)
	assert.Equal(t, 0, *jc.NumaNode)
	assert.Equal(t, "/usr/local/bin/firecracker", jc.ExecFile)
	assert.Equal(t, "/usr/bin/jailer", jc.JailerBinary)
	assert.Equal(t, "/run/sandbox-runtime/jailer", jc.ChrootBaseDir)
	assert.Equal(t, "2", jc.CgroupVersion)
	assert.NotNil(t, jc.ChrootStrategy)
	assert.Equal(t, os.Stdin, jc.Stdin)
	assert.Equal(t, os.Stdout, jc.Stdout)
	assert.Equal(t, os.Stderr, jc.Stderr)
}

func TestResolveJailerUIDGIDPrecreated(t *testing.T) {
	r, _ := newTestRuntime(t, t.TempDir())
	staticUID, staticGID := 100, 200
	jc := cfg.Jailer{UIDGID: cfg.UIDGID{Mode: "precreated", StaticUID: &staticUID, StaticGID: &staticGID}}

	uid, gid, err := r.resolveJailerUIDGID(jc, "sandbox-1")
	require.NoError(t, err)
	assert.Equal(t, 100, uid)
	assert.Equal(t, 200, gid)
}

func TestResolveJailerUIDGIDOnDemand(t *testing.T) {
	start, end := 100000, 100001
	st, err := store.Open(t.TempDir())
	require.NoError(t, err)
	r, err := NewRuntime(&cfg.Settings{
		State: t.TempDir(),
		Runtime: cfg.Runtime{Firecracker: cfg.Firecracker{Jailer: cfg.Jailer{
			Enabled:       true,
			ChrootBaseDir: t.TempDir(),
			UIDGID:        cfg.UIDGID{Mode: "on_demand", RangeStart: &start, RangeEnd: &end},
		}}},
		Network: cfg.Network{Cidr: "10.201.6.0/24", BridgeName: "sri-test1"},
	}, st)
	require.NoError(t, err)

	jc := r.cfg.Runtime.Firecracker.Jailer

	uid1, gid1, err := r.resolveJailerUIDGID(jc, "sandbox-1")
	require.NoError(t, err)
	assert.Equal(t, uid1, gid1)

	uid2, gid2, err := r.resolveJailerUIDGID(jc, "sandbox-2")
	require.NoError(t, err)
	assert.Equal(t, uid2, gid2)
	assert.NotEqual(t, uid1, uid2)

	// Range exhausted: a third allocation must fail.
	_, _, err = r.resolveJailerUIDGID(jc, "sandbox-3")
	assert.Error(t, err)

	require.NoError(t, r.jailerIDs.Release(uid1))
	uid3, _, err := r.resolveJailerUIDGID(jc, "sandbox-4")
	require.NoError(t, err)
	assert.Equal(t, uid1, uid3, "expected the released id to be reused")
}

func TestResolveJailerUIDGIDUnknownMode(t *testing.T) {
	r, _ := newTestRuntime(t, t.TempDir())
	_, _, err := r.resolveJailerUIDGID(cfg.Jailer{UIDGID: cfg.UIDGID{Mode: "bogus"}}, "sandbox-1")
	assert.Error(t, err)
}

// jailer requires --chroot-base-dir to already exist.
func TestNewRuntimeCreatesJailerChrootBaseDir(t *testing.T) {
	st, err := store.Open(t.TempDir())
	require.NoError(t, err)

	staticUID, staticGID := 1000, 1000
	chrootBaseDir := filepath.Join(t.TempDir(), "jailer")
	_, err = os.Stat(chrootBaseDir)
	require.True(t, os.IsNotExist(err), "precondition: chroot base dir must not already exist")

	_, err = NewRuntime(&cfg.Settings{
		State: t.TempDir(),
		Runtime: cfg.Runtime{Firecracker: cfg.Firecracker{Jailer: cfg.Jailer{
			Enabled:       true,
			ChrootBaseDir: chrootBaseDir,
			UIDGID:        cfg.UIDGID{Mode: "precreated", StaticUID: &staticUID, StaticGID: &staticGID},
		}}},
		Network: cfg.Network{Cidr: "10.201.7.0/24", BridgeName: "sri-test2"},
	}, st)
	require.NoError(t, err)

	info, err := os.Stat(chrootBaseDir)
	require.NoError(t, err, "expected NewRuntime to create the jailer chroot base dir")
	assert.True(t, info.IsDir())
}

func TestStartSandboxRequiresSandboxId(t *testing.T) {
	r, _ := newTestRuntime(t, t.TempDir())
	_, err := r.StartSandbox(context.Background(), &v1.StartSandboxRequest{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestStartSandboxUnknownSandbox(t *testing.T) {
	r, _ := newTestRuntime(t, t.TempDir())
	_, err := r.StartSandbox(context.Background(), &v1.StartSandboxRequest{SandboxId: "does-not-exist"})
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestStartSandboxSuccess creates a real sandbox (fake firecracker
// process and all, via CreateSandbox) and then starts it, verifying
// StartSandbox actually sent a PUT /actions to the running process'
// socket rather than just succeeding vacuously.
func TestStartSandboxSuccess(t *testing.T) {
	fakeMkfsExt4(t, filepath.Join(t.TempDir(), "args.txt"))

	reqLogPath := filepath.Join(t.TempDir(), "requests.log")
	require.NoError(t, os.Setenv(fakeFirecrackerReqLogEnv, reqLogPath))
	t.Cleanup(func() { os.Unsetenv(fakeFirecrackerReqLogEnv) })

	stateDir := t.TempDir()
	r, st := newTestRuntimeWithFakeFirecracker(t, stateDir, false)
	seedRootfsEntry(t, st, "myimage:latest")

	createResp, err := r.CreateSandbox(context.Background(), &v1.CreateSandboxRequest{
		Config: &v1.SandboxConfig{
			Metadata:  &v1.SandboxMetadata{Uid: "sandbox-1"},
			Resources: &v1.SandboxResources{StorageBytes: 4 << 30, MemoryBytes: 128 << 20},
			Rootfs:    &v1.RootFSMount{Spec: &v1.RootFSSpec{Image: "myimage:latest"}},
		},
	})
	require.NoError(t, err)

	_, err = r.StartSandbox(context.Background(), &v1.StartSandboxRequest{SandboxId: createResp.GetSandboxId()})
	require.NoError(t, err)

	got, err := os.ReadFile(reqLogPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "PUT /actions")
}
