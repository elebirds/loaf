#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

version=${VERSION:-dev-$(git rev-parse --short HEAD)-$(date -u +%Y%m%d%H%M%S)}
if [[ ! "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]{0,100}$ ]]; then
    echo "Invalid VERSION" >&2
    exit 2
fi
mkdir -p internal/bundle/assets dist
for arch in amd64 arm64; do
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
        -ldflags="-s -w -X github.com/elebirds/loaf/internal/wire.Version=$version" \
        -o "internal/bundle/assets/loaf-runner-linux-$arch" ./cmd/loaf-runner
done

targets=${LOAF_BUILD_TARGETS:-$(go env GOOS)/$(go env GOARCH)}
for target in $targets; do
    os=${target%/*}
    arch=${target#*/}
    case "$os/$arch" in
        darwin/amd64|darwin/arm64|linux/amd64|linux/arm64) ;;
        *) echo "Unsupported build target: $target" >&2; exit 2 ;;
    esac
    package="loaf_${version}_${os}_${arch}"
    mkdir -p "dist/$package"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -tags loaf_embed \
        -ldflags="-s -w -X github.com/elebirds/loaf/internal/wire.Version=$version" \
        -o "dist/$package/loaf" ./cmd/loaf
    cp LICENSE README.md README.zh-Hans.md "dist/$package/"
    tar -czf "dist/$package.tar.gz" -C "dist/$package" loaf LICENSE README.md README.zh-Hans.md
    if [[ "$target" == "$(go env GOOS)/$(go env GOARCH)" ]]; then
        cp "dist/$package/loaf" dist/loaf
    fi
    printf '%s\n' "dist/$package.tar.gz"
done
printf '%s\n' "$version" >dist/version
