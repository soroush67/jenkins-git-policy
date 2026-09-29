#!/usr/bin/env bash
# Build and test git-policy inside a Go container: no Go toolchain needed on
# the host. Dependencies are vendored, so test/build work offline after the
# first `vendor` run.
#
#   tools/build.sh vendor   # go mod tidy + vendor (needs network once)
#   tools/build.sh test     # go vet + go test
#   tools/build.sh build    # static linux/amd64 binary -> dist/
#   tools/build.sh all      # test + build
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
GO_IMAGE=${GO_IMAGE:-golang:1.24-alpine}
VERSION=$(tr -d '[:space:]' < "$ROOT/VERSION")
COMMIT=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
PKG=github.com/soroush67/git-policy/internal/version

mkdir -p "$ROOT/.cache/go-build" "$ROOT/.cache/gomod" "$ROOT/dist"

go_run() {
    docker run --rm \
        -u "$(id -u):$(id -g)" \
        -v "$ROOT:/src" -w /src \
        -e HOME=/tmp \
        -e GOCACHE=/src/.cache/go-build \
        -e GOMODCACHE=/src/.cache/gomod \
        -e GOTOOLCHAIN=local \
        -e CGO_ENABLED=0 \
        -e GOFLAGS="${GOFLAGS_OVERRIDE:--mod=vendor}" \
        "$GO_IMAGE" "$@"
}

vendor() {
    GOFLAGS_OVERRIDE=-mod=mod go_run sh -c 'go mod tidy && go mod vendor && go mod verify'
}

test() {
    go_run sh -c 'test -z "$(gofmt -l cmd internal | tee /dev/stderr)" && go vet ./... && go test -count=1 ./...'
}

build() {
    local out=dist/git-policy-${VERSION}-linux-amd64
    go_run env GOOS=linux GOARCH=amd64 go build -trimpath \
        -ldflags "-s -w -X ${PKG}.Version=${VERSION} -X ${PKG}.Commit=${COMMIT} -X ${PKG}.BuildDate=${BUILD_DATE}" \
        -o "/src/${out}" ./cmd/git-policy
    (cd "$ROOT" && sha256sum "$out" > "$out.sha256" && cat "$out.sha256")
}

case "${1:-all}" in
    vendor) vendor ;;
    test) test ;;
    build) build ;;
    all) test && build ;;
    *) echo "usage: $0 {vendor|test|build|all}" >&2; exit 2 ;;
esac
