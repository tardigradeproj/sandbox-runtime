# `pkg/rootfs/unpack_linux.go` walkthrough

This file is two small functions, `mknodEntry` and `applyPlatformMetadata`,
called from `writeEntry` in `unpack.go` (see `3.md`) for every tar entry that
needs something only Linux can do: create a device node, or restore exact
ownership/extended attributes. Before explaining what the code does, it's
worth building up *why* each syscall exists at all - the foundations.

## Foundations

### A file isn't always "a file"

On Linux, every directory entry - files, directories, symlinks, pipes,
sockets, device nodes - is backed by an **inode**, and every inode has a
**type**. The type isn't optional metadata; it's what tells the kernel's VFS
(virtual filesystem layer) *how to route I/O* when something opens that path:

- A **regular file**'s inode points at data blocks on disk. Reading it means
  reading those blocks.
- A **character device**'s inode doesn't point at any data at all. Opening it
  hands you a direct channel to a device driver, and reads/writes go straight
  to that driver, one byte (or arbitrary-sized chunk) at a time, with no
  buffering or seeking in the usual sense. `/dev/null`, `/dev/zero`,
  `/dev/urandom`, `/dev/console`, `/dev/tty` are all character devices.
- A **block device**'s inode also routes to a driver, but one that exposes
  fixed-size, randomly-addressable blocks - the abstraction a filesystem is
  built on top of. `/dev/sda` is a block device.
- A **FIFO** (named pipe) doesn't talk to any driver. Its inode just gives a
  filesystem *name* to an in-kernel pipe buffer, so two unrelated processes can
  find each other and exchange bytes without needing to be related by `fork()`.

None of these four need "a file" in the normal sense to back them - a
character device has no data to read from disk, a FIFO has no persistent
content at all. The inode is purely a named entry point into something else
the kernel already manages.

### `mknod(2)`: creating that entry point

The `mknod` syscall creates exactly one of these special inodes:

```c
int mknod(const char *path, mode_t mode, dev_t dev);
```

Two things to notice:

- **`mode` carries the type, not just permissions.** A Unix file mode is a
  bitfield: the low 12 bits are the familiar `rwxrwxrwx` + setuid/setgid/sticky
  permission bits, but the *upper* bits encode the type itself - `S_IFREG`,
  `S_IFDIR`, `S_IFLNK`, `S_IFCHR`, `S_IFBLK`, `S_IFIFO`, `S_IFSOCK`. `mknod`
  only ever creates `S_IFCHR`, `S_IFBLK`, or `S_IFIFO` nodes (regular files and
  directories are created by `open(O_CREAT)`/`mkdir`, symlinks by `symlink`) -
  but it still needs that type folded into `mode`, because that's the only way
  to tell it *which* of the three to make.
- **`dev` only means something for char/block devices.** It's the device
  number - which driver, and which instance of that driver's devices, this
  node should route to. It's ignored entirely for a FIFO, which has no driver.

### Major/minor numbers and `dev_t`

A device number is really two numbers glued together: a **major** number
(which driver - e.g. major 1 is the "mem" driver family that includes
`/dev/null` and `/dev/zero`) and a **minor** number (which specific device
within that driver - `/dev/null` is minor 3, `/dev/zero` is minor 5). The
kernel packs both into a single integer, `dev_t`, and `makedev()`/`MKDEV()` in
C (and `unix.Mkdev()` in Go) is just the bit-packing function for that.

### Ownership and `L`chown

`chown(path, uid, gid)` changes who owns a file - which matters for device
nodes too: whether a non-root process is allowed to open `/dev/console` at all
depends on its ownership and permission bits, same as any other file. The
`L`-prefixed variant, `lchown`, is the one that **doesn't follow symlinks** -
it changes ownership of the symlink itself, not whatever it points at. That
distinction matters here because a tar entry being processed might *be* a
symlink, and chasing it to chown something else entirely would be wrong.

### Extended attributes and the `SCHILY.xattr.` prefix

Beyond the standard permission/ownership metadata, Linux inodes can carry
arbitrary name/value pairs called **extended attributes** (xattrs) -
`security.selinux` (a SELinux label), `security.capability` (POSIX
capabilities), `trusted.overlay.opaque` (the OverlayFS marker from `3.md`'s
whiteout discussion) are all xattrs.

The POSIX `tar` format has no native field for these, so there's no "official"
way to carry an xattr through a tar stream. The convention that won out - used
by GNU tar, `star`, and every OCI/Docker layer tar - is to store each xattr as
a PAX extended header record under a vendor-prefixed key:
`SCHILY.xattr.<name>` → `<value>` (`SCHILY` because `star`'s author is Jörg
**Schilling**). Go's `archive/tar` surfaces these as plain string key/value
pairs in `hdr.PAXRecords`, with no special parsing - it's on the reader to
know the convention and strip the prefix back off.

## Connecting the dots: the actual code

```go
func mknodEntry(target string, hdr *tar.Header) error {
	mode := uint32(hdr.FileInfo().Mode().Perm())
	switch hdr.Typeflag {
	case tar.TypeChar:
		mode |= unix.S_IFCHR
	case tar.TypeBlock:
		mode |= unix.S_IFBLK
	case tar.TypeFifo:
		mode |= unix.S_IFIFO
	}
	dev := unix.Mkdev(uint32(hdr.Devmajor), uint32(hdr.Devminor))
	return unix.Mknod(target, mode, int(dev))
}
```

- `hdr.FileInfo().Mode()` is a Go `os.FileMode` - Go's own, portable
  encoding of file metadata, which is *not* bit-for-bit the same as a raw Unix
  mode (Go uses its own high bits to mark directories, symlinks, etc., so that
  the same type works sensibly on Windows too). `.Perm()` strips all of that
  down to just the low 9 permission bits - the part that *is* directly
  reusable as the low bits of a Unix `mode_t`.
- The `switch` then puts back the real Unix type bit by hand, because Go's
  portable encoding has no equivalent of `S_IFCHR`/`S_IFBLK`/`S_IFIFO` to give
  us - these three types don't exist as a concept outside Unix-like systems.
- `hdr.Devmajor`/`hdr.Devminor` are read straight from the tar header -
  meaning they're whatever major/minor numbers the *original image's build
  tooling* wrote into that layer when it was created. This code doesn't invent
  device numbers; it faithfully reproduces whatever the image already
  specified.
- `unix.Mkdev` packs those two into the single `dev_t` the syscall expects,
  and `unix.Mknod` is the direct, un-wrapped `mknod(2)` call from the section
  above - creating the actual character, block, or FIFO node at `target`.

This is called from `writeEntry` in `unpack.go` only for
`tar.TypeChar`/`TypeBlock`/`TypeFifo` entries - the three cases `mknod(2)`
itself covers; regular files, directories, and symlinks are handled by
ordinary `os` calls elsewhere in that same function, since those don't need a
raw syscall at all.

```go
func applyPlatformMetadata(target string, hdr *tar.Header) {
	_ = unix.Lchown(target, hdr.Uid, hdr.Gid)

	const xattrPrefix = "SCHILY.xattr."
	for k, v := range hdr.PAXRecords {
		if !strings.HasPrefix(k, xattrPrefix) {
			continue
		}
		name := strings.TrimPrefix(k, xattrPrefix)
		_ = unix.Lsetxattr(target, name, []byte(v), 0)
	}
}
```

Unlike `mknodEntry`, this one runs for **every** successfully-written entry in
`writeEntry` - files, directories, symlinks, and device nodes alike - because
ownership and xattrs are properties any of them can carry, not something
specific to device nodes.

- `unix.Lchown(target, hdr.Uid, hdr.Gid)` restores whatever UID/GID the image
  declared for that path - `hdr.Uid`/`hdr.Gid` come straight off the tar
  header, same principle as the device numbers above: reproduce what the image
  actually said, don't substitute our own assumption. `L`chown specifically,
  for the reason above - `writeEntry` may be extracting a symlink, and must
  never follow it.
- The loop walks every PAX record on the header, keeps only the ones with the
  `SCHILY.xattr.` prefix from the foundations section, strips that prefix back
  off to recover the real attribute name, and calls `unix.Lsetxattr` (again
  the symlink-safe, non-following variant) to restore it.
- Both calls' errors are discarded (`_ =`). The doc comment explains why:
  `Lchown`ing to an arbitrary UID, and `Mknod`-ing a device node at all,
  require privilege (`CAP_CHOWN`, `CAP_MKNOD`) that a non-root developer
  running this locally simply won't have. Since this runtime's one real
  deployment target - launching Firecracker VMs - already requires running as
  root for raw KVM/TAP device access, failing to restore ownership/xattrs in
  that environment would be a genuine bug; failing on a non-root laptop during
  local development is an expected, harmless no-op. Swallowing the error lets
  the common development case succeed while production, running with the
  privilege it already needs anyway, still gets the real behavior.

## Why this is its own, Linux-only file

Every function here - `unix.Mknod`, `unix.Mkdev`, `unix.Lchown`,
`unix.Lsetxattr` - is a thin wrapper around a syscall that simply doesn't
exist as such outside Linux (macOS has its own, differently-shaped `mknod`;
Windows has none of this concept at all). That's why this logic lives in
`unpack_linux.go` behind a `//go:build linux` tag, with `unpack_other.go`
providing a stub that errors clearly on `mknodEntry` and no-ops
`applyPlatformMetadata` everywhere else. As `3.md` covers, this split is what
lets the rest of `pkg/rootfs` be developed and tested on macOS, while the one
platform that actually matters in production - Linux, because Firecracker
*is* Linux/KVM - gets the real, faithful extraction.
