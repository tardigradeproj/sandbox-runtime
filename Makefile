PROTO_DIR := pkg/api/runtime/v1
PROTO := $(PROTO_DIR)/api.proto

.PHONY: proto
proto:
	protoc \
		--proto_path=. \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		$(PROTO)

.PHONY: proto-tools
proto-tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

.PHONY: test
test:
	go test ./...

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

.PHONY: images
images: images-busybox images-ubuntu

.PHONY: images-busybox
images-busybox:
	docker build -f $(IMAGES_DIR)/Dockerfile.busybox -t sandbox-rootfs-busybox $(IMAGES_DIR)

.PHONY: images-ubuntu
images-ubuntu:
	docker build -f $(IMAGES_DIR)/Dockerfile.ubuntu -t sandbox-rootfs-ubuntu $(IMAGES_DIR)
