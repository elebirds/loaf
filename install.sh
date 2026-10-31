#!/bin/sh
# Install the newest published Loaf release, including prereleases.
set -eu

fail() { printf 'loaf install: %s\n' "$*" >&2; exit 1; }

if [ "${1:-}" = --help ] || [ "${1:-}" = -h ]; then
    cat <<'HELP'
Install Loaf for the current user. Re-running replaces the installed package.

  sh install.sh

Optional environment variables:
  LOAF_INSTALL_DIR       Package directory (default: ~/.local/share/loaf/cli)
  LOAF_VERSION           Install an exact release instead of the newest one
  LOAF_NO_MODIFY_PATH=1  Leave shell startup files unchanged
HELP
    exit 0
fi
[ "$#" -eq 0 ] || fail 'Use sh install.sh --help for installation options.'

for tool in curl tar; do
    command -v "$tool" >/dev/null 2>&1 || fail "Install $tool first, then run this installer again."
done
if command -v sha256sum >/dev/null 2>&1; then
    checksum() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
    checksum() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
    fail 'Install sha256sum or shasum first, then run this installer again.'
fi
case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail 'Supported systems: macOS, Linux and WSL. On Windows, use install.ps1.' ;;
esac
case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) fail 'Supported architectures: x86-64 and ARM64.' ;;
esac

install_dir=${LOAF_INSTALL_DIR:-"$HOME/.local/share/loaf/cli"}
case "$install_dir" in
    /*) ;;
    *) fail 'LOAF_INSTALL_DIR must be an absolute path.' ;;
esac
case "$install_dir" in
    *:*|*'
'*) fail 'The installation path cannot contain a colon or newline (PATH separators).' ;;
esac
temporary=$(mktemp -d "${TMPDIR:-/tmp}/loaf-install.XXXXXX")
staged=
trap 'rm -rf "$temporary"; if [ -n "$staged" ]; then rm -f "$staged"; fi' 0
trap 'exit 1' HUP INT TERM
download() {
    curl --fail --silent --show-error --location --connect-timeout 15 --max-time 180 \
        "$1" --output "$2" || fail "Could not download $1. Check the network and release version."
}
version=${LOAF_VERSION:-}
if [ -z "$version" ]; then
    download "${LOAF_RELEASES_URL:-https://api.github.com/repos/elebirds/loaf/releases}?per_page=1" "$temporary/release.json"
    # The public list includes prereleases; /releases/latest excludes them.
    # Split fields so compact and pretty-printed API responses work alike.
    version=$(tr ',' '\n' <"$temporary/release.json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | sed -n '1p')
fi
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || fail 'No valid published release found. Set LOAF_VERSION to an existing release tag to select one.'
archive="loaf_${version}_${os}_${arch}.tar.gz"
base="${LOAF_DOWNLOAD_URL:-https://github.com/elebirds/loaf/releases/download}/$version"
printf 'Installing Loaf %s (%s/%s)\n' "$version" "$os" "$arch"
download "$base/$archive" "$temporary/$archive"
download "$base/loaf_${version}_checksums.txt" "$temporary/checksums.txt"
expected=$(awk -v name="$archive" '$2 == name { print tolower($1) }' "$temporary/checksums.txt")
[ "${#expected}" -eq 64 ] || fail "Missing or invalid checksum for $archive. Nothing was installed."
case "$expected" in *[!0-9a-f]*) fail 'Invalid release checksum. Nothing was installed.' ;; esac
[ "$(checksum "$temporary/$archive")" = "$expected" ] || fail 'Checksum mismatch. Nothing was installed; the existing executable is unchanged.'
mkdir "$temporary/package"
tar -xzf "$temporary/$archive" -C "$temporary/package"
[ -f "$temporary/package/loaf" ] || fail 'The release archive does not contain loaf.'

mkdir -p "$install_dir"
# Replace the executable inode: a running CLI must not be truncated in place.
staged=$(mktemp "$install_dir/.loaf.XXXXXX")
cp "$temporary/package/loaf" "$staged"
chmod 755 "$staged"
mv -f "$staged" "$install_dir/loaf"
rm "$temporary/package/loaf"
cp -R "$temporary/package/." "$install_dir/"
printf 'Installed to %s\n' "$install_dir"

# Keep literal paths safe in both POSIX-shell and fish double-quoted strings.
quoted=$(printf '%s' "$install_dir" | sed 's/[\\"`$]/\\&/g')
path_line="case \":\$PATH:\" in *:\"$quoted\":*) ;; *) export PATH=\"$quoted:\$PATH\" ;; esac"
activation="export PATH=\"$quoted:\$PATH\""
case "${SHELL:-}" in
    */fish) activation="contains -- \"$quoted\" \$PATH; or set -gx PATH \"$quoted\" \$PATH" ;;
esac
append_path() {
    profile=$1
    mkdir -p "$(dirname "$profile")"
    if [ ! -f "$profile" ] || ! grep -Fqx "$path_line" "$profile"; then
        printf '\n# Loaf\n%s\n' "$path_line" >>"$profile" || fail "Could not update $profile. Add $install_dir to PATH manually."
    fi
    printf 'PATH configured in %s\n' "$profile"
}
if [ "${LOAF_NO_MODIFY_PATH:-0}" != 1 ]; then
    case "${SHELL:-/bin/sh}" in
        */zsh) append_path "${ZDOTDIR:-$HOME}/.zshrc" ;;
        */bash)
            append_path "$HOME/.bashrc"
            if [ -f "$HOME/.bash_profile" ]; then
                append_path "$HOME/.bash_profile"
            elif [ -f "$HOME/.bash_login" ]; then
                append_path "$HOME/.bash_login"
            else
                append_path "$HOME/.profile"
            fi
            ;;
        */fish)
            path_line=$activation
            append_path "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/loaf.fish"
            ;;
        *) append_path "$HOME/.profile" ;;
    esac
fi
if [ "${LOAF_NO_MODIFY_PATH:-0}" = 1 ]; then
    printf '\nFor this terminal, run:\n  %s\n' "$activation"
else
    printf '\nOpen a new terminal, or run:\n  %s\n' "$activation"
fi
printf '\nThen try: loaf --help\nDocs: %s/manual\n' "$install_dir"
if ! command -v ssh >/dev/null 2>&1 || ! command -v rsync >/dev/null 2>&1; then
    printf 'Remote workflows also need OpenSSH and rsync on PATH; see the installation manual.\n'
fi
