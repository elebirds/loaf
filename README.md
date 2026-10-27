# Loaf

[English](README.md) · [简体中文](README.zh-Hans.md)

**Write code locally. Run recoverable experiments on your SSH machines.**

Loaf connects a local research project to an existing Linux machine. Submit the
command you already use; return later for its status, logs, and outputs.

- **Run your current code.** Synchronize working files, including uncommitted
  changes, and give each experiment an independent working copy.
- **Disconnect without losing the experiment.** Accepted tasks run under a remote
  supervisor, independently of your laptop and SSH connection.
- **Find the result again.** A saved task ID stays bound to its original machine,
  logs, exit result, and output selection.

Loaf reuses OpenSSH, your keys, SSH Config, and jump hosts. Optional AutoDL
acquisition is explicit; running an experiment never rents a machine.

## Install

These commands describe the current source. For a downloaded release, use the
README shipped with that archive; older versions use different command names.

Download a matching archive from [Releases](https://github.com/elebirds/loaf/releases)
and extract it into a new directory. From the extracted directory:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 loaf "$HOME/.local/bin/loaf"
"$HOME/.local/bin/loaf" --version
```

Add `$HOME/.local/bin` to PATH if needed. Clients target macOS/Linux on amd64 and
arm64. You need OpenSSH and rsync locally, and Linux with rsync remotely.
Experiment dependencies are your own environment, not installed by Loaf.

To use the new command layout directly from this checkout, build with the
Go version pinned in `mise.toml`:

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --version
```

See [installation and verification](manual/en/installation.md) and
[upgrading](manual/en/upgrading.md). A complete build embeds the Linux runners;
plain `go install` is not a complete installation.

## Your first experiment

Use the CPU example included in both the archive and the repository. It needs
only `/bin/sh` remotely. Replace `lab-gpu` with your existing SSH Host alias
(or a quoted SSH connection such as `"ssh -p 2222 user@host"`).

From the extracted archive or repository:

```bash
cd examples/cpu
loaf machine add lab --ssh lab-gpu
loaf machine use lab
loaf run
loaf status
loaf logs
loaf pull
```

The example configures its command and waits briefly for completion. Expect
`succeeded` and a downloaded `outputs/result.txt` containing `result=42` under
`loaf-results/<run-id>/`. `pull` prints the exact destination.

`machine add` saves a connection; it does not connect. Establish host trust with native
SSH before noninteractive use. Static targets need keys, an SSH agent, or working
native connection sharing for batch authentication.

In your own project, no initialization or configuration file is required:

```bash
loaf run -- python3 train.py
```

`run` checks prerequisites, synchronizes source, prepares an independent copy,
and submits the background task. No preceding `check` or `sync` is required.
The remote interpreter and dependencies must already be available.

## Everyday commands

| What you want | Shortest call |
| --- | --- |
| Run the command in `loaf.toml` | `loaf run` |
| Run a program with literal arguments | `loaf run -- python3 train.py` |
| Preview without connecting or writing state | `loaf run --dry-run -- python3 train.py` |
| List recent tasks | `loaf status` |
| Query one task | `loaf status ID` |
| Read the latest task's log | `loaf logs` |
| Follow its log | `loaf logs -f` |
| Stop that task | `loaf stop` |
| Browse its outputs | `loaf outputs` |
| Download its outputs | `loaf pull` |
| List saved machines | `loaf machine` |
| Select a machine for this project | `loaf machine use NAME` |

Use an explicit task ID when several experiments are involved:

```bash
loaf status r_0123456789abcdef01234567
loaf logs r_0123456789abcdef01234567 -f
loaf pull r_0123456789abcdef01234567
```

IDs in examples are placeholders. Omitting an ID selects this project's latest
submitted or submission-uncertain task for logs, stop, outputs, and pull.
Changing the selected machine does not redirect saved tasks.

`--wait 10s` is an observation budget, not a training time limit. Closing a log
stream or waiting client does not stop accepted work. If submission is
unconfirmed, query its saved ID rather than starting a replacement.

See the [complete command list with minimal calls](manual/en/commands.md), or
ask the CLI directly:

```bash
loaf help run
loaf help machine
```

## Use Loaf from an agent

Give your agent the self-contained [Loaf Skill](skills/loaf/SKILL.md). It starts
with existing machines and explains how to preserve IDs and recover uncertain
results. The Skill's `references/` directory must travel with it.

```bash
loaf run --wait 10s --json -- python3 train.py
# Save data.run_id, including when the operation returns an error.
loaf status r_0123456789abcdef01234567 --json
```

Finite commands return one versioned JSON result. `logs -f`, `exec`, and
`cloud watch` stream NDJSON when `--json` is set. `ok` describes the CLI
operation; inspect task `status` and `exit_code` for the experiment result.

Discover parameters without parsing help prose:

```bash
loaf help run --json
```

Read the [JSON contract and result examples](manual/en/json.md). JSON mode does
not prompt. Permission to run an experiment does not authorize paid acquisition,
whole-instance shutdown, or deletion.

## Go further

- [Usage guide](manual/en/guide.md): waiting, synchronization, outputs, cleanup,
  foreground commands, and service forwarding.
- [Configuration](manual/en/configuration.md): project discovery, preferences,
  output selection, explicit zero/false values, and precedence.
- [AutoDL resources](manual/en/cloud.md): login, connect, acquire, clone, and
  recover a saved request.
- [Troubleshooting](manual/en/troubleshooting.md): diagnose a failure and continue
  the same task or resource operation.
- [Upgrading](manual/en/upgrading.md): command changes and matching old CLIs.
- [Development](manual/en/development.md): build, tests, architecture, and the
  isolated Linux SSH fixture.

## License

[MIT](LICENSE).
