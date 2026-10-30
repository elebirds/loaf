#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

release=$(cat VERSION)
version=${VERSION:-${release}-dev.$(git rev-parse --short HEAD).$(date -u +%Y%m%d%H%M%S)}
if [[ ! "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]{0,100}$ ]]; then
    echo "Invalid VERSION" >&2
    exit 2
fi
mkdir -p internal/bundle/assets dist
staging=$(mktemp -d "$PWD/dist/.package.XXXXXX")
trap 'rm -rf "$staging"' EXIT
for arch in amd64 arm64; do
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
        -ldflags="-s -w -X github.com/elebirds/loaf/internal/wire.Version=$version" \
        -o "internal/bundle/assets/loaf-runner-linux-$arch" ./cmd/loaf-runner
done

targets=${LOAF_BUILD_TARGETS:-$(go env GOOS)/$(go env GOARCH)}
archives=()
for target in $targets; do
    os=${target%/*}
    arch=${target#*/}
    case "$os/$arch" in
        darwin/amd64|darwin/arm64|linux/amd64|linux/arm64|windows/amd64) ;;
        *) echo "Unsupported build target: $target" >&2; exit 2 ;;
    esac
    package="loaf_${version}_${os}_${arch}"
    directory="$staging/$package"
    executable=loaf
    if [[ "$os" == windows ]]; then executable=loaf.exe; fi
    mkdir -p "$directory"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -tags loaf_embed \
        -ldflags="-s -w -X github.com/elebirds/loaf/internal/wire.Version=$version" \
        -o "$directory/$executable" ./cmd/loaf
    cp LICENSE README.md README.zh-Hans.md "$directory/"
    cp -R skills examples manual "$directory/"
    go run ./scripts/package -out "$directory" -version "$version" "$directory/$executable" \
        internal/bundle/assets/loaf-runner-linux-amd64 internal/bundle/assets/loaf-runner-linux-arm64
    if [[ "$os" == windows ]]; then
        archive="$package.zip"
        go run ./scripts/package -zip "$staging/$archive" "$directory"
    else
        archive="$package.tar.gz"
        go run ./scripts/package -tar-gz "$staging/$archive" "$directory"
    fi
    mv "$staging/$archive" "dist/$archive"
    archives+=("dist/$archive")
    if [[ "$target" == "$(go env GOOS)/$(go env GOARCH)" ]]; then
        # Replace the inode instead of overwriting a previously executed client.
        mv "$directory/$executable" "dist/$executable"
    fi
    printf '%s\n' "dist/$archive"
done
go run ./scripts/package -checksums "dist/loaf_${version}_checksums.txt" "${archives[@]}"
printf '%s\n' "$version" >dist/version
