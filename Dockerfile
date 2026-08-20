# synclet container image — multi-platform without QEMU/binfmt
#
# The build stage always runs natively (--platform=$BUILDPLATFORM) and
# cross-compiles via GOOS/GOARCH; the runtime stage has zero RUN steps, so
# `docker buildx build --platform linux/amd64,linux/arm64` needs no emulation.
#
# Build:  docker build -t synclet:dev .
# Multi:  docker buildx build --platform linux/amd64,linux/arm64 -t <repo>/synclet:tag --push .
# Run:    docker run --rm \
#           -v $PWD/config.yaml:/etc/synclet/config.yaml:ro \
#           -v synclet-data:/var/lib/synclet \
#           --env-file .env \
#           synclet:dev --once

FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/synclet ./cmd/synclet
# Runtime assets come from the (debian-based) build image — the final stage
# stays RUN-free: TLS roots, timezone data and a minimal user/group database.
RUN mkdir -p /out/assets/etc/synclet /out/assets/etc/ssl/certs \
             /out/assets/usr/share /out/assets/var/lib/synclet && \
    cp /etc/ssl/certs/ca-certificates.crt /out/assets/etc/ssl/certs/ && \
    cp -r /usr/share/zoneinfo /out/assets/usr/share/ && \
    printf 'root:x:0:0:root:/root:/sbin/nologin\nsynclet:x:65532:65532:synclet:/var/lib/synclet:/sbin/nologin\n' \
      > /out/assets/etc/passwd && \
    printf 'root:x:0:\nsynclet:x:65532:\n' > /out/assets/etc/group

FROM alpine:3.22
LABEL org.opencontainers.image.title="synclet" \
      org.opencontainers.image.source="https://github.com/keveon/synclet" \
      org.opencontainers.image.description="Lightweight config-driven table replication between databases" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/assets/ /
# Example config ships with the image for reference; real config is always mounted
COPY --from=build /src/config.example.yaml /etc/synclet/config.example.yaml
USER 65532
WORKDIR /var/lib/synclet
VOLUME ["/var/lib/synclet"]
COPY --from=build /out/synclet /usr/local/bin/synclet
ENTRYPOINT ["synclet"]
