# syntax=docker/dockerfile:1.6

# Build on the native arch (BUILDPLATFORM) and cross-compile to
# TARGETARCH. This avoids QEMU-emulated Go builds, which for arm64 on
# an amd64 runner are ~5-10× slower than native cross-compilation.
FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG GITHASH=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    BUILDTIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION} -X main.gitHash=${GITHASH} -X main.buildTime=${BUILDTIME}" \
        -o /bin/mm2tg ./cmd/mm2tg

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata \
    && adduser -D -u 10001 mm2tg
USER mm2tg
COPY --from=builder /bin/mm2tg /bin/mm2tg
ENTRYPOINT ["/bin/mm2tg"]
