package observability

// Structured log field keys, so call sites share one spelling instead
// of repeating string literals.
const (
	SandboxID    = "sandbox.id"
	SandboxImage = "sandbox.image"
	OverlayPath  = "sandbox.overlay_path"
	SocketPath   = "sandbox.socket_path"
	LogPath      = "sandbox.log_path"

	RootfsImage  = "rootfs.image"
	RootfsDigest = "rootfs.digest"
	RootfsPath   = "rootfs.path"
	RootfsSize   = "rootfs.size"

	ConfigRootfs      = "config.rootfs"
	ConfigVersion     = "config.version"
	GRPCListenAddress = "grpc.listen_address"
	FirecrackerBin    = "firecracker.bin"
)
