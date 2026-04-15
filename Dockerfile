# syntax=docker/dockerfile:1.6
FROM golang:alpine AS builder

RUN apk --no-cache add git

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    GITHASH="$(git log --pretty=format:'%h' -n 1 2>/dev/null || echo unknown)" \
    && BUILDTIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    && CGO_ENABLED=0 go build \
        -ldflags "-s -w -X main.gitHash=${GITHASH} -X main.buildTime=${BUILDTIME}" \
        -o /bin/mm2tg ./cmd/mm2tg

FROM alpine
RUN apk --no-cache add ca-certificates tzdata \
    && adduser -D -u 10001 mm2tg
USER mm2tg
COPY --from=builder /bin/mm2tg /bin/mm2tg
ENTRYPOINT ["/bin/mm2tg"]
