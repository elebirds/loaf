# Loaf

[English](README.md) | [简体中文](README.zh-Hans.md)

**本地写代码，使用已有的 SSH 科研机器。**

Loaf 在项目目录中管理远端机器地址、项目选择和环境检查。继续使用已有的 SSH 配置、密钥和跳板机，无须向另一套服务托管凭据。

> 早期开发版本：目前可从源码使用 `add`、`use`、`check` 和 `version`。源码同步、后台运行 CLI、日志及结果下载仍在开发中。

## 从源码构建

需要 Go 1.27.1、Git、OpenSSH 和 rsync。Go 版本固定在 `mise.toml`；使用 mise 时，请先检查该文件再信任项目配置。

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

构建在 `dist/` 生成本地可执行文件及压缩包。目前尚无正式安装包，也可以将 `dist/loaf` 放入已在 PATH 中的目录。

## 配置科研机器

在科研项目目录中执行：

```bash
# 保存平台提供的 SSH 连接
loaf add autodl --ssh "ssh -p 12345 root@example.com"
loaf use autodl

# 检查连接、远端目录、工具和磁盘
loaf check
```

已有 SSH Host 别名时，使用 `loaf add lab --ssh lab-gpu`。跳板配置仍由 SSH Config 管理，也接受 `-J`、密钥、端口和显式 `-F` 配置文件。粘贴内容只能包含连接参数，不能夹带远端命令或 Shell 表达式。修改已有别名时使用 `--replace`。

`add` 仅在本地保存连接，`check` 才会实际连接。JSON 和非交互终端检查不会询问密码或主机信任；必要时先使用原生 SSH 建立访问。Loaf 不保存密码，也不关闭主机密钥验证。

Python、uv 和 GPU 工具属于可选检查项，缺失时不会让能够执行 CPU 程序的机器检查失败。缺少 rsync、不支持的平台或不可写的远端位置则会报告问题。

## 项目配置

项目按最近的 `loaf.toml`、Git 工作树根目录、当前目录的顺序识别，无须 `init`。可选的项目配置可以指定机器和远端目录：

```toml
target = "autodl"
remote_dir = "~/projects/my-research"
```

目标选择顺序为 `--target`、当前项目的 `loaf use` 设置、TOML 中的 `target`。个人选择不会改写项目 TOML。未设置 `remote_dir` 时，各项目使用稳定 ID，在目标的远端基目录下拥有独立路径。可以通过 `loaf add ... --remote-dir '~/projects'` 设置基目录。

用户配置保存在 `~/.config/loaf/config.toml`，项目标识和个人选择保存在 `~/.local/state/loaf/index.json`。`XDG_CONFIG_HOME` 和 `XDG_STATE_HOME` 可以覆盖这些位置。

## 多语言与结构化输出

帮助、交互提示、错误和检查结果原生支持英文与简体中文：

```bash
loaf --lang zh-Hans --help
LOAF_LANG=en loaf check --json
```

语言选择顺序为 `--lang`、`LOAF_LANG`、用户配置中的 `language`；默认 `auto` 使用系统语言。语言偏好不属于共享项目配置。

核心命令支持 `--json`，包含 `schema_version: 1`、稳定的 `ok` 字段，以及结构化数据或错误码。人类可读的 `message` 和 `hint` 可以随语言变化，命令名、字段和错误码保持不变；外部诊断保留原文。操作退出码：0 表示成功，2 表示参数或配置错误，1 表示连接或其他操作失败。

## 开发

```bash
go run ./scripts/format.go    # Go 代码使用四空格格式化
bash scripts/check.sh        # 单元、静态、竞态、格式及漏洞检查
bash scripts/integration.sh  # 真实 SSH 与 Linux 进程测试，需要 Docker
```

集成环境使用临时密钥，验证发行包中的连接检查、跳板机和带空格的 SSH 配置路径，不接触科研服务器。

Loaf 不负责租用机器、分配 GPU、自动修复环境或替代实验追踪工具。

## 许可证

[MIT](LICENSE)
