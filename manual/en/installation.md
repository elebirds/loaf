# Install Loaf

[Home](../../README.md) · [Command reference](commands.md) · [简体中文](../zh-Hans/installation.md)

Download the archive for your platform and its checksums file from [Releases](https://github.com/elebirds/loaf/releases). Filenames use `darwin` for macOS, `linux` for Linux, `arm64` for Apple Silicon/ARM64, and `amd64` for Intel/AMD x86-64. Extract the archive into a new directory.

Use the manuals inside that archive for its available commands. The repository's
`main` branch can contain features newer than the latest published download;
`VERSION` records a target, not proof of a published release. To try `main`, use
the source build below. Before replacing a CLI used by existing tasks, follow
[the upgrade steps](upgrading.md) and keep its matching binary.

Archives contain one executable, both READMEs, the Skill, examples, `BUILD.json`, and third-party license texts. From an extracted archive directory:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 loaf "$HOME/.local/bin/loaf"
"$HOME/.local/bin/loaf" version --json
```

Add that directory to PATH if needed. Running an archive needs OpenSSH and rsync locally, not Go. The remote machine needs Linux and rsync; prepare the experiment's Python environment separately.

Clients target macOS/Linux amd64 and arm64, plus native Windows x64. WSL2 Ubuntu x64 uses the Linux amd64 archive. Runners remain Linux amd64/arm64. Native Windows ARM64, Windows remote execution and UNC network shares are outside this release's scope. Windows 10 runtime acceptance is still pending; a cross-build alone is not runtime validation.

`BUILD.json` records the source revision, modified-tree state, Go version, target, and dependency notice inventory. `loaf version --json` exposes the corresponding build identity; missing source metadata is reported as unknown, not clean. Set `archive` to the exact archive filename and `version` to the software version in that filename. Then run the check from the directory containing the archive and its checksums file:

```bash
# archive is the exact filename of your archive
grep -F "  $archive" "loaf_${version}_checksums.txt" | shasum -a 256 -c -
```

Published release archives carry GitHub build-provenance attestations, verifiable with `gh attestation verify "$archive" --repo elebirds/loaf`. See [GitHub's verification guide](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations#verifying-artifact-attestations-with-the-github-cli). Local builds include metadata and download checksums, not a GitHub-signed attestation.

### Native Windows

Use the `windows_amd64.zip` archive on a local NTFS drive. It contains `loaf.exe` and the same manuals, examples and notices as the Unix archives. Extract into a new directory with PowerShell `Expand-Archive`, and run `./loaf.exe version --json` from it. Add its directory to your user PATH to run `loaf` from PowerShell or CMD. Verify the ZIP using `Get-FileHash -Algorithm SHA256` against its entry in the checksums file.

Install [MSYS2](https://www.msys2.org/) and update it using `pacman -Syu` in the MSYS2 terminal, reopening that terminal and repeating the update when requested. Then install the paired transport tools:

```bash
pacman -S --needed openssh rsync
```

In PowerShell, point Loaf at that installation (adjust the path if needed):

```powershell
$env:LOAF_MSYS2_ROOT = 'C:\msys64'
loaf version --json
loaf machine add lab --ssh lab-gpu
loaf machine use lab
loaf check --json
```

For CMD use `set "LOAF_MSYS2_ROOT=C:\msys64"`. These settings apply to the current terminal; save the environment variable in your Windows user settings if desired. When it is absent, Loaf locates `rsync.exe` on PATH and requires its sibling MSYS2 OpenSSH and `cygpath.exe`. It never downloads dependencies or combines Windows OpenSSH, Git SSH, Cygwin or WSL tools. SSH aliases, keys, agents and jump hosts must work in this selected MSYS2 installation. In SSH config files, use forward slashes and quote paths containing spaces; explicitly use `ssh -F C:/path/config host` when needed.

Windows opens a separate SSH connection for each operation, including cloud operations. Cloud connection lookups always use the frozen instance identity. There is no private SSH master on this path. Password authentication uses a private native askpass helper; ordinary static SSH authentication and host verification remain OpenSSH-owned. Private file credential storage is covered by Windows tests; Windows Credential Manager still requires an isolated-account acceptance run.

Keep projects, state and download destinations on local NTFS drives. Unicode, spaces and cross-drive destinations are supported. Downloads reject Windows reserved names, illegal characters and case collisions before transfer. Relative symlinks require actual symlink privileges on the destination filesystem; unsupported links fail instead of becoming copies. Native Windows supports the event store, Bridge, Webhook delivery and remote queues. Unix-socket Agent adapters report `DESTINATION_UNSUPPORTED`.

### WSL2

Inside Ubuntu, install distribution tools with `sudo apt install openssh-client rsync`, then use the Linux amd64 archive and the Unix installation steps above. Run the Linux `loaf` inside WSL, including for projects under `/mnt/c`; do not invoke `loaf.exe` for this path. Keep state and credentials in the Linux home directory, separate from the Windows store. Do not point XDG overrides at native Windows state. Linux private SSH connection reuse remains enabled. Agent adapters must be reachable and verified inside the same WSL environment; a Windows desktop session is not automatically accessible.

### Build from source

Requires Go 1.27.3, Git, OpenSSH, and rsync. The Go version is pinned in `mise.toml`; review that file before trusting it if you use mise.

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

The build embeds Linux amd64 and arm64 runners and produces `dist/loaf` (`dist/loaf.exe` on Windows), a platform archive, and `loaf_<version>_checksums.txt`. Windows archives are ZIP; Unix archives are tar.gz. Default build identities use the release in `VERSION` plus a development revision and timestamp; an explicit `VERSION` environment variable selects an exact software version. For other build targets, set `LOAF_BUILD_TARGETS`, for example `linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64`. On Windows, run this same script from Git Bash or MSYS2 Bash with the pinned Windows Go on PATH. The race checks additionally require a supported C compiler, such as MSYS2 UCRT64 GCC (`mingw-w64-ucrt-x86_64-gcc`).
