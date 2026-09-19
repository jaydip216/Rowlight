#!/bin/sh
set -eu

version=${VERSION:-dev}
release_dir=${RELEASE_DIR:-release}
commit=$(git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ldflags="-s -w -X main.version=$version -X main.commit=$commit -X main.buildDate=$build_date"
mkdir -p "$release_dir"

for arch in arm64 amd64; do
  package="rowlight-${version}-darwin-${arch}"
  stage=$(mktemp -d "${TMPDIR:-/tmp}/rowlight-release.XXXXXX")
  trap 'rm -rf "$stage"' EXIT INT TERM

  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" \
    GOCACHE="${GOCACHE:-/tmp/rowlight-gocache}" \
    GOMODCACHE="${GOMODCACHE:-/tmp/rowlight-gomodcache}" \
    go build -trimpath -ldflags="$ldflags" -o "$stage/rowlight" .
  cp README.md "$stage/README.md"
  tar -czf "$release_dir/${package}.tar.gz" -C "$stage" rowlight README.md
  rm -rf "$stage"
  trap - EXIT INT TERM
done

(
  cd "$release_dir"
  shasum -a 256 rowlight-"$version"-darwin-*.tar.gz > SHA256SUMS
)
