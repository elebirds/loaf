# Loaf

[English](README.md) · [简体中文](README.zh-Hans.md)

**本地写代码，在已有的 SSH 机器上运行可找回的实验。**

Loaf 把本地科研项目连接到已有的 Linux 机器。提交原本准备运行的命令，
之后凭任务 ID 查看状态、日志并取回输出。

- **运行当前代码。** 同步工作文件，包括未提交修改；每个实验使用独立工作副本。
- **断开连接也能继续。** 已接管的任务由远端监督进程管理，不依赖本地终端或 SSH 连接。
- **找回同一次结果。** 任务 ID 固定绑定原机器、日志、退出结果和输出选择。

Loaf 继续使用 OpenSSH、密钥、SSH Config 和跳板机。
AutoDL 租用是可选的显式操作；运行实验不会隐式租卡。

## 安装

本页描述当前源码的命令体系。下载发行包时，请使用包内 README；
旧版本使用不同的命令名称。

从 [Releases](https://github.com/elebirds/loaf/releases) 下载匹配平台的压缩包，
解压到新目录。在解压后的目录执行：

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 loaf "$HOME/.local/bin/loaf"
"$HOME/.local/bin/loaf" --version
```

按需将 `$HOME/.local/bin` 加入 PATH。客户端构建目标为 macOS/Linux 的
amd64、arm64；本地需要 OpenSSH 和 rsync，远端需要 Linux 和 rsync。
实验依赖使用你已有的环境，Loaf 不代为安装。

要从当前工作区使用新命令，使用 `mise.toml` 固定的 Go 版本构建：

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --version
```

详见[安装与校验](manual/zh-Hans/installation.md)和[升级说明](manual/zh-Hans/upgrading.md)。
完整构建会内置 Linux runner；单独 `go install` 不是完整安装路径。

## 第一次实验

使用发行包和源码都包含的 CPU 示例，远端只需 `/bin/sh`。
将 `lab-gpu` 换成已有的 SSH Host 别名，也可以传入完整连接：
`"ssh -p 2222 user@host"`。

在解压后的发行包或源码目录中执行：

```bash
cd examples/cpu
loaf machine add lab --ssh lab-gpu
loaf machine use lab
loaf run
loaf status
loaf logs
loaf pull
```

示例已配置执行命令，并短暂等待完成。预期任务状态为成功，
下载到 `loaf-results/<run-id>/` 下的 `outputs/result.txt` 包含 `result=42`。
`pull` 会打印准确的本地位置。

`machine add` 只保存连接，不连接远端。非交互使用前，请通过原生 SSH 建立主机信任。
静态机器的批量认证需要密钥、SSH agent 或已生效的原生连接复用。

在自己的项目中，无须初始化或编写配置文件，可以直接提交：

```bash
loaf run -- python3 train.py
```

`run` 自动检查前提、同步源码、准备独立副本并提交后台任务，
不需要预先执行 `check` 或 `sync`。远端解释器和依赖应已准备好。

## 日常使用速查

| 你想做什么 | 最简调用 |
| --- | --- |
| 运行 `loaf.toml` 中的命令 | `loaf run` |
| 原样传入程序及参数 | `loaf run -- python3 train.py` |
| 不连接、不写状态地预览提交 | `loaf run --dry-run -- python3 train.py` |
| 列出最近任务 | `loaf status` |
| 查询一个任务 | `loaf status ID` |
| 查看最近任务的日志 | `loaf logs` |
| 持续查看其日志 | `loaf logs -f` |
| 停止该任务 | `loaf stop` |
| 浏览其输出 | `loaf outputs` |
| 下载其输出 | `loaf pull` |
| 列出保存的机器 | `loaf machine` |
| 为当前项目选择机器 | `loaf machine use NAME` |

涉及多个实验时，明确填写任务 ID：

```bash
loaf status r_0123456789abcdef01234567
loaf logs r_0123456789abcdef01234567 -f
loaf pull r_0123456789abcdef01234567
```

示例 ID 是占位值。`logs`、`stop`、`outputs`、`pull` 省略 ID 时，
选择当前项目最近的已提交或提交未确认任务。
更换所选机器不会将已有任务改向。

`--wait 10s` 是观察预算，不是训练时限。关闭日志流或等待客户端不会停止已接管任务。
提交未确认时，应查询保存的原 ID，而不是重新提交替代任务。

完整清单见[命令与最简用法](manual/zh-Hans/commands.md)，也可以直接查询：

```bash
loaf help run
loaf help machine
```

## 让 Agent 使用 Loaf

向 Agent 提供自包含的 [Loaf Skill](skills/loaf/SKILL.md)。
它从已有机器开始，说明如何保留 ID、判断结果和恢复未确认操作。
复制 Skill 时须同时保留其 `references/` 目录。

```bash
loaf run --wait 10s --json -- python3 train.py
# 保存 data.run_id，包括本次操作返回错误时。
loaf status r_0123456789abcdef01234567 --json
```

有限命令返回一个带版本的 JSON 结果；`logs -f`、`exec`、`cloud watch`
在 JSON 模式下输出 NDJSON 流。`ok` 表示 CLI 操作结果，
实验结果应读取 `status` 和 `exit_code`。

参数信息也可以直接结构化读取：

```bash
loaf help run --json
```

详见 [JSON 契约与结果示例](manual/zh-Hans/json.md)。JSON 模式不弹出交互提示。
运行实验的授权不包含付费获取、整机关机或数据删除。

## 进一步阅读

- [使用指南](manual/zh-Hans/guide.md)：等待、同步、输出、清理、前台命令和服务转发。
- [配置参考](manual/zh-Hans/configuration.md)：项目发现、偏好、输出、显式零值/false 和优先级。
- [AutoDL 资源](manual/zh-Hans/cloud.md)：登录、接入、获取、克隆与原请求恢复。
- [故障恢复](manual/zh-Hans/troubleshooting.md)：定位失败并继续处理同一任务或资源操作。
- [升级说明](manual/zh-Hans/upgrading.md)：命令变化与匹配旧任务的 CLI。
- [开发说明](manual/zh-Hans/development.md)：构建、测试、架构及隔离 Linux SSH fixture。

## 许可证

[MIT](LICENSE)。
