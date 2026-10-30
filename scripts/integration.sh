#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
docker info >/dev/null
docker build -t loaf-sshd-test:local -f tests/integration/Dockerfile.sshd .
docker build -t loaf-credentials-test:local -f tests/integration/Dockerfile.credentials .
arch=$(docker image inspect loaf-sshd-test:local --format '{{.Architecture}}')
target=$(go env GOOS)/$(go env GOARCH)
targets="$target"
if [[ "$target" != "linux/$arch" ]]; then targets="$targets linux/$arch"; fi
LOAF_BUILD_TARGETS="${LOAF_BUILD_TARGETS:-$targets}" bash scripts/build.sh
version=$(cat dist/version)
host=$(go env GOOS)_$(go env GOARCH)
temporary=$(mktemp -d "$PWD/dist/.integration.XXXXXX")
trap 'rm -rf "$temporary"' EXIT
mkdir "$temporary/package"
executable=loaf
test_executable=ssh-host.test
local_path() { printf '%s' "$1"; }
if [[ "$target" == windows/* ]]; then
    executable=loaf.exe
    test_executable=ssh-host.test.exe
    local_path() { cygpath -m -- "$1"; }
    LOAF_TEST_ARCHIVE="$(local_path "$PWD/dist/loaf_${version}_${host}.zip")" LOAF_TEST_DIRECTORY="$(local_path "$temporary/package")" \
        powershell.exe -NoProfile -NonInteractive -Command 'Expand-Archive -LiteralPath $env:LOAF_TEST_ARCHIVE -DestinationPath $env:LOAF_TEST_DIRECTORY -ErrorAction Stop'
else
    tar -xzf "dist/loaf_${version}_${host}.tar.gz" -C "$temporary/package"
fi
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -o "$temporary/remote.test" ./internal/remote
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -o "$temporary/credentials.test" ./internal/credentials
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -o "$temporary/cli.test" ./internal/cli
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -ldflags="-X github.com/elebirds/loaf/internal/wire.Version=$version" -o "$temporary/ssh.test" ./internal/ssh
go test -c -ldflags="-X github.com/elebirds/loaf/internal/wire.Version=$version" -o "$temporary/$test_executable" ./internal/ssh
LOAF_INTEGRATION=1 LOAF_HOST_SSH_TEST="$(local_path "$temporary/$test_executable")" LOAF_BIN="$(local_path "$temporary/package/$executable")" LOAF_REMOTE_TEST="$(local_path "$temporary/remote.test")" \
    LOAF_CREDENTIAL_TEST="$(local_path "$temporary/credentials.test")" LOAF_CLI_TEST="$(local_path "$temporary/cli.test")" \
    LOAF_SSH_TEST="$(local_path "$temporary/ssh.test")" \
    LOAF_LINUX_ARCHIVE="$(local_path "$PWD/dist/loaf_${version}_linux_${arch}.tar.gz")" go test -count=1 -v -tags loaf_embed \
    -ldflags="-X github.com/elebirds/loaf/internal/wire.Version=$version" "$@" ./tests/integration
