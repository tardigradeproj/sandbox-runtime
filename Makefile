PROTO_DIR := pkg/api/runtime/v1
PROTO := $(PROTO_DIR)/api.proto

# GO_PACKAGES is every real Go package in this module, scoped to avoid
# recursing into ui/frontend/node_modules: it's a JS/TS project (see
# ui/frontend/package.json), and its node_modules can contain stray
# vendored .go files from npm dependencies (e.g. protobuf-es's own
# tooling) that `go build ./...`/`go test ./...` would otherwise try
# to compile as part of this module. ./ui/frontend (no "/...") still
# covers the embed.go package itself, just not its node_modules below it.
GO_PACKAGES := ./cmd/... ./internal/... ./pkg/... ./ui/frontend ./ui/server/...

# proto regenerates both the Go bindings (pkg/api/runtime/v1) and the
# frontend's TypeScript client (ui/frontend/src/gen) from one
# api.proto, via buf -- see buf.gen.yaml. Requires `make proto-tools`
# and `cd ui/frontend && npm install` to have been run first (buf
# resolves the TS plugins from ui/frontend/node_modules/.bin).
.PHONY: proto
proto:
	buf generate

.PHONY: proto-tools
proto-tools:
	go install github.com/bufbuild/buf/cmd/buf@v1.75.0
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
	go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.21.1

.PHONY: console-build
console-build:
	cd ui/frontend && npm ci && npm run build

.PHONY: console-lint
console-lint:
	cd ui/frontend && npm ci && npm run lint && npm run typecheck

.PHONY: build
build: console-build
	go build -o bin/sandbox-runtime ./cmd

CONFIG         ?= /etc/sandbox-runtime/config.toml
CONSOLE_LISTEN ?= 127.0.0.1:9090
BACKBONE_ADDR  ?= unix:///var/run/sandbox-runtime.sock

.PHONY: run-grpc
run-grpc:
	go run ./cmd grpc --config $(CONFIG)

.PHONY: run-console
run-console:
	go run ./cmd console --listen $(CONSOLE_LISTEN) --backbone-addr $(BACKBONE_ADDR)

.PHONY: test
test:
	go test $(GO_PACKAGES)

.PHONY: vet
vet:
	go vet $(GO_PACKAGES)

# test-integration exercises the real mkfs.erofs/erofs-utils toolchain,
# which is Linux-only, inside a disposable container (see .local/) so it
# also runs on non-Linux development machines. The container stays up
# after the test run (see .local/docker-compose.yaml); tear it down with
# test-integration-down when you're done.
.PHONY: test-integration
test-integration:
	docker compose -f .local/docker-compose.yaml up --build

.PHONY: test-integration-down
test-integration-down:
	docker compose -f .local/docker-compose.yaml down

IMAGES_DIR := images

# See issue4.md: every tier sources from Ubuntu's own official
# minimal cloud image (not the bloated server one, not a
# container-oriented systemd base), downloaded fresh into a fixed
# per-tier filename right before each build.
CLOUD_IMAGES_BASE := https://cloud-images.ubuntu.com
MINIMAL_24_04_URL := $(CLOUD_IMAGES_BASE)/minimal/releases/noble/release/ubuntu-24.04-minimal-cloudimg-amd64-root.tar.xz
MINIMAL_26_04_URL := $(CLOUD_IMAGES_BASE)/minimal/releases/resolute/release/ubuntu-26.04-minimal-cloudimg-amd64-root.tar.xz

# Alpine's own official minirootfs (https://alpinelinux.org/downloads/),
# one version only -- see Dockerfile.alpine.
ALPINE_URL := https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/x86_64/alpine-minirootfs-3.24.4-x86_64.tar.gz

.PHONY: images
images: images-minimal images-ubuntu-systemd-24.04 images-ubuntu-systemd-26.04 \
	images-ubuntu-sandboxd-24.04 images-ubuntu-sandboxd-26.04 images-alpine

.PHONY: images-minimal
images-minimal:
	curl -fsSL $(MINIMAL_24_04_URL) -o $(IMAGES_DIR)/minimal-rootfs.tar.xz
	docker build --build-arg ROOTFS_TARBALL=minimal-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.minimal -t sandbox-rootfs-minimal $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-rootfs.tar.xz

# Two variants per Ubuntu version (see issue4.md): -systemd keeps
# systemd and runs sandboxd as one of its units; -sandboxd-init skips
# systemd entirely and runs sandboxd as PID 1 directly, the
# AGENT_INIT=yes shape Kata Containers uses for kata-agent.

.PHONY: images-ubuntu-systemd-24.04
images-ubuntu-systemd-24.04:
	curl -fsSL $(MINIMAL_24_04_URL) -o $(IMAGES_DIR)/minimal-24.04-rootfs.tar.xz
	docker build --build-arg ROOTFS_TARBALL=minimal-24.04-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.ubuntu-systemd -t sandbox-rootfs-ubuntu-systemd-24.04 $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-24.04-rootfs.tar.xz

.PHONY: images-ubuntu-systemd-26.04
images-ubuntu-systemd-26.04:
	curl -fsSL $(MINIMAL_26_04_URL) -o $(IMAGES_DIR)/minimal-26.04-rootfs.tar.xz
	docker build --build-arg ROOTFS_TARBALL=minimal-26.04-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.ubuntu-systemd -t sandbox-rootfs-ubuntu-systemd-26.04 $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-26.04-rootfs.tar.xz

.PHONY: images-ubuntu-sandboxd-24.04
images-ubuntu-sandboxd-24.04:
	curl -fsSL $(MINIMAL_24_04_URL) -o $(IMAGES_DIR)/minimal-24.04-rootfs.tar.xz
	docker build --build-arg ROOTFS_TARBALL=minimal-24.04-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.ubuntu-sandboxd-init -t sandbox-rootfs-ubuntu-sandboxd-24.04 $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-24.04-rootfs.tar.xz

.PHONY: images-ubuntu-sandboxd-26.04
images-ubuntu-sandboxd-26.04:
	curl -fsSL $(MINIMAL_26_04_URL) -o $(IMAGES_DIR)/minimal-26.04-rootfs.tar.xz
	docker build --build-arg ROOTFS_TARBALL=minimal-26.04-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.ubuntu-sandboxd-init -t sandbox-rootfs-ubuntu-sandboxd-26.04 $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-26.04-rootfs.tar.xz

.PHONY: images-alpine
images-alpine:
	curl -fsSL $(ALPINE_URL) -o $(IMAGES_DIR)/alpine-minirootfs.tar.gz
	docker build --build-arg ROOTFS_TARBALL=alpine-minirootfs.tar.gz \
		-f $(IMAGES_DIR)/Dockerfile.alpine -t sandbox-rootfs-alpine $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/alpine-minirootfs.tar.gz

# images-*-push push a single-arch (amd64) image under a
# multi-platform-shaped tag, so PullRootFS's existing "no child with
# platform linux/amd64" check still finds what it expects. NOT a real
# multi-platform manifest: ROOTFS_TARBALL is one pre-downloaded amd64
# tarball baked in via ADD, so building --platform linux/arm64 here
# would silently produce a broken arm64 image (ADD doesn't re-fetch
# or re-select per target arch the way an apt-get install under QEMU
# emulation would). Real arm64 support needs each arch's own
# -arm64-root.tar.xz downloaded and selected per platform, not done
# here -- PLATFORMS is deliberately amd64-only until that exists.
# REGISTRY_IMAGE has no useful default across this many tiers --
# always pass it explicitly, e.g.
#   make images-ubuntu-systemd-24.04-push REGISTRY_IMAGE=docker.io/you/name:tag
PLATFORMS ?= linux/amd64
REGISTRY_IMAGE ?=

.PHONY: images-minimal-24.04-push
images-minimal-24.04-push:
	curl -fsSL $(MINIMAL_24_04_URL) -o $(IMAGES_DIR)/minimal-24.04-rootfs.tar.xz
	docker buildx build --platform $(PLATFORMS) --build-arg ROOTFS_TARBALL=minimal-24.04-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.minimal -t $(REGISTRY_IMAGE) --push $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-24.04-rootfs.tar.xz

.PHONY: images-minimal-26.04-push
images-minimal-26.04-push:
	curl -fsSL $(MINIMAL_26_04_URL) -o $(IMAGES_DIR)/minimal-26.04-rootfs.tar.xz
	docker buildx build --platform $(PLATFORMS) --build-arg ROOTFS_TARBALL=minimal-26.04-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.minimal -t $(REGISTRY_IMAGE) --push $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-26.04-rootfs.tar.xz

.PHONY: images-ubuntu-systemd-24.04-push
images-ubuntu-systemd-24.04-push:
	curl -fsSL $(MINIMAL_24_04_URL) -o $(IMAGES_DIR)/minimal-24.04-rootfs.tar.xz
	docker buildx build --platform $(PLATFORMS) --build-arg ROOTFS_TARBALL=minimal-24.04-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.ubuntu-systemd -t $(REGISTRY_IMAGE) --push $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-24.04-rootfs.tar.xz

.PHONY: images-ubuntu-systemd-26.04-push
images-ubuntu-systemd-26.04-push:
	curl -fsSL $(MINIMAL_26_04_URL) -o $(IMAGES_DIR)/minimal-26.04-rootfs.tar.xz
	docker buildx build --platform $(PLATFORMS) --build-arg ROOTFS_TARBALL=minimal-26.04-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.ubuntu-systemd -t $(REGISTRY_IMAGE) --push $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-26.04-rootfs.tar.xz

.PHONY: images-ubuntu-sandboxd-24.04-push
images-ubuntu-sandboxd-24.04-push:
	curl -fsSL $(MINIMAL_24_04_URL) -o $(IMAGES_DIR)/minimal-24.04-rootfs.tar.xz
	docker buildx build --platform $(PLATFORMS) --build-arg ROOTFS_TARBALL=minimal-24.04-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.ubuntu-sandboxd-init -t $(REGISTRY_IMAGE) --push $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-24.04-rootfs.tar.xz

.PHONY: images-ubuntu-sandboxd-26.04-push
images-ubuntu-sandboxd-26.04-push:
	curl -fsSL $(MINIMAL_26_04_URL) -o $(IMAGES_DIR)/minimal-26.04-rootfs.tar.xz
	docker buildx build --platform $(PLATFORMS) --build-arg ROOTFS_TARBALL=minimal-26.04-rootfs.tar.xz \
		-f $(IMAGES_DIR)/Dockerfile.ubuntu-sandboxd-init -t $(REGISTRY_IMAGE) --push $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/minimal-26.04-rootfs.tar.xz

.PHONY: images-alpine-push
images-alpine-push:
	curl -fsSL $(ALPINE_URL) -o $(IMAGES_DIR)/alpine-minirootfs.tar.gz
	docker buildx build --platform $(PLATFORMS) --build-arg ROOTFS_TARBALL=alpine-minirootfs.tar.gz \
		-f $(IMAGES_DIR)/Dockerfile.alpine -t $(REGISTRY_IMAGE) --push $(IMAGES_DIR)
	rm -f $(IMAGES_DIR)/alpine-minirootfs.tar.gz
