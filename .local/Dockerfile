FROM quay.io/k0sproject/bootloose-ubuntu24.04

ARG GO_VERSION=1.26.7
ARG TARGETARCH

RUN apt-get update -qq && \
    apt-get install -y -qq --no-install-recommends ca-certificates erofs-utils e2fsprogs && \
    rm -rf /var/lib/apt/lists/* && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" -o /tmp/go.tar.gz && \
    tar -C /usr/local -xzf /tmp/go.tar.gz && \
    rm -f /tmp/go.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /src
