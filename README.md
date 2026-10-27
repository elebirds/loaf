# Loaf

**Code here. Run anywhere.**

[![CI](https://github.com/elebirds/loaf/actions/workflows/ci.yml/badge.svg)](https://github.com/elebirds/loaf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/elebirds/loaf?include_prereleases)](https://github.com/elebirds/loaf/releases)
[![MIT License](https://img.shields.io/github/license/elebirds/loaf)](LICENSE)

[English](README.md) · [简体中文](README.zh-Hans.md)

Loaf is a lightweight, local-first remote compute tool for developers,
researchers, and coding agents. Keep your editor and workflow on your laptop;
put the compute on a lab server or cloud GPU.

Loaf brings machine access, code sync, background execution, and result retrieval
into one CLI. Run your current working files, disconnect while the job continues,
and come back to the same task ID for its status, logs, and outputs.

## Why Loaf

- **Simple things should be simple.** Pick a machine, run a command, get the result.
- **Complexity is opt-in.** Start with an SSH connection; add project settings,
  batch operations, and cloud resources as you need them.
- **Reliable by default.** Each task has its own working copy and a saved identity
  that follows it through submission, observation, and result retrieval.
- **Agent-friendly, not Agent-dependent.** Readable terminal output for people;
  structured results and discoverable parameters for agents.
- **Own the compute lifecycle, not the workload logic.** Loaf handles resources,
  execution, and delivery so you can focus on the program you're running.
- **Reuse before reinventing.** Build on OpenSSH, rsync, and the remote environments
  you already use.
- **One product, progressive capabilities.** Move from a single experiment to
  machine and AutoDL resource management in the same workflow.

## Get started

[Install Loaf](manual/en/installation.md) on macOS or Linux. Have a Linux machine
ready that you can reach with SSH; Loaf uses your existing SSH configuration.

The archive and source tree both include a small CPU example. From either one,
replace `lab-gpu` with your SSH Host alias and run:

```bash
cd examples/cpu
loaf machine add lab --ssh lab-gpu
loaf machine use lab
loaf run
loaf status
loaf logs
loaf pull
```

The task finishes with `succeeded`. `pull` prints the download directory;
`loaf-results/<run-id>/outputs/result.txt` contains `result=42`.

### Run your own project

From your project directory, select a machine and use the command you'd run there:

```bash
loaf machine use lab
loaf run -- python3 train.py
```

Loaf checks the machine, syncs your working files, and starts an independent
background task. Follow the logs or bring the outputs home:

```bash
loaf logs -f
loaf pull
```

See the [complete command list](manual/en/commands.md) for the shortest calls,
or use `loaf help run` to explore a command.

## Work with an agent

Give your coding agent the [Loaf Skill](skills/loaf/SKILL.md), together with its
`references/` directory. It covers running an experiment, interpreting its result,
and continuing from a saved task ID.

```bash
loaf run --wait 10s --json -- python3 train.py
```

Save `data.run_id` for follow-up calls. Agents can discover parameters with
`loaf help run --json`; the [JSON reference](manual/en/json.md) explains result
fields, streaming events, and recovery.

## Documentation

- [Command reference](manual/en/commands.md) · [Usage guide](manual/en/guide.md)
- [Configuration](manual/en/configuration.md) · [AutoDL resources](manual/en/cloud.md)
- [Troubleshooting](manual/en/troubleshooting.md) · [Upgrading](manual/en/upgrading.md)
- [Development](manual/en/development.md)

## License

[MIT](LICENSE).
