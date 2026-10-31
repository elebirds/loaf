#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
docker info >/dev/null
docker build -t loaf-sshd-test:local -f tests/integration/Dockerfile.sshd .
docker build -t loaf-credentials-test:local -f tests/integration/Dockerfile.credentials .
arch=$(docker image inspect loaf-sshd-test:local --format '{{.Architecture}}')
target=$(go env GOOS)/$(go env GOARCH)
mkdir -p dist
temporary=$(mktemp -d "$PWD/dist/.integration.XXXXXX")
trap 'rm -rf "$temporary"' EXIT
if [[ "${LOAF_V1_COMPAT:-}" == 1 ]]; then
    if [[ -z "${LOAF_COMPAT_OLD_BIN:-}" ]]; then
        mkdir "$temporary/baseline"
        baseline_tag=$(git tag --list v1.0.0)
        if [[ -n "$baseline_tag" && "${GITHUB_REF:-}" != refs/tags/v1.0.0 ]]; then
            # After the first release, test against its frozen public artifact.
            # A failed download fails this gate; it never substitutes current code.
            host=${target/\//_}
            curl --fail --show-error --silent --location \
                "https://github.com/elebirds/loaf/releases/download/v1.0.0/loaf_v1.0.0_${host}.tar.gz" \
                --output "$temporary/baseline.tar.gz"
            tar -xzf "$temporary/baseline.tar.gz" -C "$temporary/baseline" loaf
        else
            # Bootstrap before v1.0.0 exists, and while verifying its initial tag.
            VERSION=v1.0.0 LOAF_BUILD_TARGETS="$target" bash scripts/build.sh
            cp dist/loaf "$temporary/baseline/loaf"
        fi
        export LOAF_COMPAT_OLD_BIN="$temporary/baseline/loaf"
    fi
    test -x "$LOAF_COMPAT_OLD_BIN"
    export VERSION="${VERSION:-v1.1.0}"
fi
targets="$target"
if [[ "$target" != "linux/$arch" ]]; then targets="$targets linux/$arch"; fi
LOAF_BUILD_TARGETS="${LOAF_BUILD_TARGETS:-$targets}" bash scripts/build.sh
version=$(cat dist/version)
host=$(go env GOOS)_$(go env GOARCH)
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
