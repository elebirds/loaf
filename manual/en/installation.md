# Install Loaf

[Home](../../README.md) · [Command reference](commands.md) · [简体中文](../zh-Hans/installation.md)

Download the archive for your platform and its checksums file from [Releases](https://github.com/elebirds/loaf/releases). Filenames use `darwin` for macOS, `linux` for Linux, `arm64` for Apple Silicon/ARM64, and `amd64` for Intel/AMD x86-64. Extract the archive into a new directory.

Archives contain one executable, both READMEs, the Skill, examples, `BUILD.json`, and third-party license texts. From an extracted archive directory:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 loaf "$HOME/.local/bin/loaf"
"$HOME/.local/bin/loaf" version --json
```

Add that directory to PATH if needed. Running an archive needs OpenSSH and rsync locally, not Go. The remote machine needs Linux and rsync; prepare the experiment's Python environment separately.

Clients target macOS/Linux amd64 and arm64; runners target Linux amd64 and arm64. A cross-build alone is not runtime validation.

`BUILD.json` records the source revision, modified-tree state, Go version, target, and dependency notice inventory. `loaf version --json` exposes the corresponding build identity; missing source metadata is reported as unknown, not clean. Set `archive` to the exact archive filename and `version` to the software version in that filename. Then run the check from the directory containing the archive and its checksums file:

```bash
# archive is the exact filename of your archive
grep -F "  $archive" "loaf_${version}_checksums.txt" | shasum -a 256 -c -
```

Published release archives carry GitHub build-provenance attestations, verifiable with `gh attestation verify "$archive" --repo elebirds/loaf`. See [GitHub's verification guide](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations#verifying-artifact-attestations-with-the-github-cli). Local builds include metadata and download checksums, not a GitHub-signed attestation.

### Build from source

Requires Go 1.27.3, Git, OpenSSH, and rsync. The Go version is pinned in `mise.toml`; review that file before trusting it if you use mise.

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

The build embeds Linux amd64 and arm64 runners and produces `dist/loaf`, a platform archive, and `loaf_<version>_checksums.txt`. Default build identities use the release in `VERSION` plus a development revision and timestamp; an explicit `VERSION` environment variable selects an exact software version. For other build targets, set `LOAF_BUILD_TARGETS`, for example `linux/amd64 linux/arm64 darwin/amd64 darwin/arm64`.
