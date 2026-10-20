#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
docker info >/dev/null
docker build -t loaf-sshd-test:local -f tests/integration/Dockerfile.sshd .
bash scripts/build.sh
version=$(cat dist/version)
host=$(go env GOOS)_$(go env GOARCH)
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
tar -xzf "dist/loaf_${version}_${host}.tar.gz" -C "$temporary"
LOAF_INTEGRATION=1 LOAF_BIN="$temporary/loaf" go test -count=1 -v -tags loaf_embed \
    -ldflags="-X github.com/elebirds/loaf/internal/wire.Version=$version" ./tests/integration
