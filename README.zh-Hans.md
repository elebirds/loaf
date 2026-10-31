# Loaf

**Code here. Run anywhere.**

[![CI](https://github.com/elebirds/loaf/actions/workflows/ci.yml/badge.svg)](https://github.com/elebirds/loaf/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/elebirds/loaf?include_prereleases)](https://github.com/elebirds/loaf/releases)
[![MIT License](https://img.shields.io/github/license/elebirds/loaf)](LICENSE)

[English](README.md) · [简体中文](README.zh-Hans.md)

Loaf 是面向开发者、科研人员与 Coding Agent 的轻量化、本地优先远程计算工具。
在熟悉的编辑器里写代码，让实验室服务器或云 GPU 承担计算。

Loaf 把机器接入、代码同步、后台执行和结果回收串成一个命令行工作流。
运行当前工作区的代码，断开连接去做别的事，再凭同一个任务 ID 找回状态、日志与产物。

## 为什么是 Loaf

- **简单的事就该简单。** 选一台机器，运行命令，取回结果。
- **复杂度按需引入。** 从一个 SSH 连接开始，按需使用项目配置、批量操作与云资源。
- **默认可靠。** 每个任务都有独立工作副本和保存的身份，贯穿提交、观察与结果回收。
- **对 Agent 友好，也为人而做。** 人读清晰的终端输出，Agent 读结构化结果和参数信息。
- **管理计算生命周期，让你专注程序本身。** 资源、执行和结果交付交给 Loaf，业务逻辑由你掌握。
- **复用成熟工具。** 继续使用 OpenSSH、rsync 和已经熟悉的远端环境。
- **一个产品，渐进使用。** 从单次实验到机器与 AutoDL 资源管理，沿用同一套工作流。

## 开始使用

在 macOS、Linux 或 WSL2 上[安装 Loaf](manual/zh-Hans/installation.md)；
原生 Windows x64 目前为预览目标。准备一台可通过 SSH 访问的 Linux 机器，
Loaf 沿用你已有的 SSH 配置。

发行包和源码都包含一个小型 CPU 示例。在其中任一目录下，
将 `lab-gpu` 换成你的 SSH Host 别名，然后执行：

```bash
cd examples/cpu
loaf machine add lab --ssh lab-gpu
loaf machine use lab
loaf run --wait 10s
loaf logs
loaf pull
```

任务完成后状态为 `succeeded`。`pull` 会打印下载目录；
`loaf-results/<run-id>/outputs/result.txt` 中包含 `result=42`。

你和 Agent 使用相同的命令。需要比较 GPU、预算和镜像时，
`loaf cloud acquire -i` 提供专用交互流程。普通操作保持直接调用；
裸 `loaf` 和 `loaf -i` 显示帮助。

### 运行自己的项目

在项目目录中选择机器，提交你原本准备在远端运行的命令：

```bash
loaf machine use lab
loaf run -- python3 train.py
```

Loaf 检查机器、同步工作文件，并启动独立的后台任务。
接下来可以持续查看日志，或把产物取回本地：

```bash
loaf logs -f
loaf pull
```

[完整命令列表](manual/zh-Hans/commands.md)汇总了最简用法，
也可以通过 `loaf help run` 了解一个命令。

## 与 Agent 一起工作

把 [Loaf Skill](skills/loaf/SKILL.md) 及其 `references/` 目录交给 Coding Agent。
它说明如何运行实验、判读结果，以及凭保存的任务 ID 继续工作。

```bash
loaf run --wait 10s --json -- python3 train.py
```

保存 `data.run_id`，用于后续查询。Agent 可以通过 `loaf help run --json`
读取参数信息；[JSON 参考](manual/zh-Hans/json.md)介绍结果字段、流式事件与恢复方式。

## 兼容性

兼容承诺从 `v1.0.0` 正式版开始：新正式 `1.x` 客户端继续管理受支持的旧正式 `1.x` 工作。
开发版、`0.x` 与 RC 状态不导入、不迁移。替换仍用于管理活跃工作的开发版前，
请先阅读[升级说明](manual/zh-Hans/upgrading.md)。

## 文档

- [命令参考](manual/zh-Hans/commands.md) · [使用指南](manual/zh-Hans/guide.md)
- [通知与共享机器队列](manual/zh-Hans/async.md)
- [配置参考](manual/zh-Hans/configuration.md) · [AutoDL 资源](manual/zh-Hans/cloud.md)
- [故障恢复](manual/zh-Hans/troubleshooting.md) · [升级说明](manual/zh-Hans/upgrading.md)
- [开发说明](manual/zh-Hans/development.md)

## 许可证

[MIT](LICENSE)。
