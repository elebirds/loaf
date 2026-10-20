# Loaf

[English](README.md) | [简体中文](README.zh-Hans.md)

**本地写代码，使用已有的 SSH 科研机器。**

Loaf 将当前工作代码同步到已有 SSH 机器，运行断线后仍能继续的实验，并保存任务 ID、日志和退出结果。继续使用已有的 SSH 配置、密钥和跳板机，无须向另一套服务托管凭据。

> 早期开发版本：目前可从源码使用 `add`、`use`、`check`、`sync`、`run`、`ps`、`logs` 和 `version`。公开的停止与结果下载命令尚未提供。

## 从源码构建

需要 Go 1.27.1、Git、OpenSSH 和 rsync。Go 版本固定在 `mise.toml`；使用 mise 时，请先检查该文件再信任项目配置。

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

构建会内置 Linux amd64、arm64 远端封装，并在 `dist/` 生成本地可执行文件及包含 Agent Skill 和示例的压缩包。目前尚无正式安装包，也可以将 `dist/loaf` 放入已在 PATH 中的目录。

## 配置科研机器

在科研项目目录中执行：

```bash
# 保存平台提供的 SSH 连接
loaf add autodl --ssh "ssh -p 12345 root@example.com"
loaf use autodl

# 可选：检查连接、远端目录、工具和磁盘
loaf check
```

已有 SSH Host 别名时，使用 `loaf add lab --ssh lab-gpu`。跳板配置仍由 SSH Config 管理，也接受 `-J`、密钥、端口和显式 `-F` 配置文件。粘贴内容只能包含连接参数，不能夹带远端命令或 Shell 表达式。修改已有别名时使用 `--replace`。

`add` 仅在本地保存连接，`check` 才会实际连接。JSON 和非交互终端检查不会询问密码或主机信任；必要时先使用原生 SSH 建立访问。Loaf 不保存密码，也不关闭主机密钥验证。

### 显式检查远端封装部署

```bash
loaf check --runner
```

普通 `check` 只读取远端环境。`--runner` 会额外将匹配的内置封装上传到私有、带版本的 Loaf 目录，并验证版本；不启动实验，也不安装 Python、uv、CUDA 或项目依赖。

Python、uv 和 GPU 工具属于可选检查项，缺失时不会让能够执行 CPU 程序的机器检查失败。缺少 rsync、不支持的平台或不可写的远端位置则会报告问题。

## 运行实验

在项目目录中提交原本准备在远端运行的命令：

```bash
loaf run -- python3 train.py
loaf ps
loaf logs
```

`run` 自动检查执行前提、部署匹配封装、同步工作源码，并在同一项目锁内复制独立工作目录，随后启动脱离连接的监督进程并返回任务 ID。每次运行前无须另行执行 `check`、`sync`、初始化项目或编写启动脚本。后续同步不改变已有任务的代码。

接管成功不表示训练成功。关闭本地终端或 SSH 后，仍可凭 ID 查询：

```bash
loaf ps <任务ID>
loaf logs <任务ID> -f
```

`ps` 默认查询当前项目最近 20 个已提交或提交未确认任务，可用 `--limit` 调整；指定 ID 时只查询该任务。部分查询失败会保留其他成功结果，不会把训练标成失败。`logs` 省略 ID 时选择最近一次已提交或提交未确认任务，不会退回更旧任务；明确的检查、同步失败不替换最近任务。已有任务保留提交时的连接、远端位置和封装版本，不随项目默认值改变，查询不能用 `--target` 改向。

### 短任务：有界等待结果

```bash
loaf run --wait 10s --json -- python3 smoke.py
loaf ps <任务ID> --wait 30s --json
```

从远端确认接管后计算等待预算：窗口内结束时，一次返回终态、退出结果和有限日志；尚未结束时，返回已观察到的状态和原任务 ID，任务继续运行。`--wait 0s` 在接管后返回，最长可等待 5 分钟。这是观察预算，不是训练时限；取消观察不会停止已接管任务。

普通模式显示准备阶段，并在 `run --wait` 期间持续显示日志。JSON 模式只返回一份有限结果，不混入进度文字。持续日志使用 NDJSON：

```bash
loaf logs <任务ID> --tail 100 --json
loaf logs <任务ID> -f --json
```

日志默认取最后 100 行，日志文本最多 256 KiB，`log.truncated` 表示省略了输出。持续读取产生 `data.type = "chunk"`，结束时为 `"end"`，任务查询结果在 `data.run` 中；流错误为 `"error"`，带普通错误对象。偏移量按原日志字节计算。程序输出不翻译、不解析成指标；及时看到日志还需要程序主动刷新输出。

提交回执丢失时，Loaf 查询同一个已保存 ID 一次。仍无法确认就返回 `SUBMISSION_UNKNOWN`、原 ID 和操作退出码 3。此时应查询原任务，不要自动另开一次运行。

### 可选的项目准备命令

```toml
[run]
command = "uv run python train.py"
setup = "uv sync"                 # 每次任务前显式执行
wait = "10s"

[run.env]
EXPERIMENT_NAME = "baseline"

[logs]
tail = 100
```

设置默认命令后可直接 `loaf run`。`run --` 后的显式 argv 替换默认命令，不再经过额外 Shell 解释；TOML 的 `run.command`、`run.setup` 是 `/bin/sh -c` 命令。setup 每个任务执行一次，失败则不启动训练；其 `cd`、导出变量和环境激活不会延续到独立的训练进程，应使用明确解释器或 `uv run`。项目 `run.env` 同时作用于二者，Loaf 查询不回显变量值，程序自身仍可能打印它们。

Loaf 不自动安装 Python、uv，不猜测环境激活，也不上传数据集，请显式准备。数据和共享环境可放在 `cache/` 旁边，并通过绝对路径引用，因为执行目录是 `<remote_dir>/runs/<run-id>/work/`。本阶段输出仍保存在其中，由原生工具取回；提交时保存输出路径选择，供后续下载命令使用。

可先运行[无依赖 CPU 示例](examples/cpu/)或[显式准备示例](examples/setup/)。Agent 可使用简短的 [Loaf Skill](skills/loaf/SKILL.md)，不需要读取内部文件、PID 或锁。

## 同步工作代码

在项目目录中，先预览选中的文件，再上传：

```bash
loaf sync --dry-run
loaf sync
```

预览完全在本地进行，不需要配置目标、连接 SSH 或部署封装，也不会创建项目状态。实际同步会检查目标、部署匹配的内置封装，然后调用系统 rsync 通过 OpenSSH 传输；不会安装实验依赖或启动训练。

源码写入 `<remote_dir>/cache/`。首次同步要求项目目录不存在或为空，后续同步检查项目标记。不要选择已经放有数据或环境的科研目录；这些内容应放在 `cache/` 旁边，而非里面。

默认的 **镜像模式（mirror）** 只在 `cache/` 内清理已删除或新被排除的源码，不影响项目其他目录或已有运行副本。项目锁防止源码更新与运行副本准备互相干扰。传输失败或取消后，缓存不能用于新运行，直到显式重新同步成功；已有运行不会因此停止。缓存采用增量更新，不是整个目录的原子替换。

### 选择上传内容

- Git 项目通过原生 Git 选择已跟踪的工作文件及未被忽略的新文件，包括未提交修改，保留 Git 跟踪以及嵌套忽略、否定规则的语义。非 Git 项目读取嵌套 `.gitignore`。
- Loaf 还会排除虚拟环境、常见缓存、`.git`、`.env`/`.env.*`、常见私钥名称、`*.pem`、`*.key`、`*.pt`、`*.pth`、`*.ckpt`、`*.safetensors` 和 `loaf-results/`，即使它们已被 Git 跟踪。配置的输出路径也被排除，默认是 `outputs/`。
- 保留指向已选项目文件或目录的安全相对符号链接，不遍历目录链接。绝对、越界、失效及目标未选中的链接会跳过并报告。嵌套仓库与子模块不展开，请单独同步。
- `sync.exclude` 追加 Gitignore 风格的正向排除规则，不接受用 `!` 恢复默认排除项。预览显示排除结果和原因。默认规则按名称匹配，不是凭据扫描器。

`selected_bytes` 是选中普通文件的大小之和，不是网络传输量；`changed_files` 统计实际传输文件内容及变更的链接目标，不计删除和仅时间戳、权限变化。预览选择当前工作路径，不会在继续编辑时冻结本地文件。

## 项目配置

项目按最近的 `loaf.toml`、Git 工作树根目录、当前目录的顺序识别，无须 `init`。可选的项目配置可以指定机器、远端目录和同步策略：

```toml
target = "autodl"
remote_dir = "~/projects/my-research"

[sync]
mode = "mirror"            # 改为 update 可保留远端已删除或被排除的旧文件
respect_gitignore = true
exclude = ["data/", "checkpoints/"]

[outputs]
paths = ["outputs", "checkpoints"]
```

目标选择顺序为 `--target`、当前项目的 `loaf use` 设置、TOML 中的 `target`。个人选择不会改写项目 TOML。未设置 `remote_dir` 时，各项目使用稳定 ID，在目标的远端基目录下拥有独立路径。可以通过 `loaf add ... --remote-dir '~/projects'` 设置基目录。

用户配置保存在 `~/.config/loaf/config.toml`，项目标识和个人选择保存在 `~/.local/state/loaf/index.json`，私有任务位置索引保存在同目录的 `runs.json`；执行记录和日志仍保存在绑定的远端机器上。`XDG_CONFIG_HOME` 和 `XDG_STATE_HOME` 可以覆盖这些位置。

个人同步默认值放在已有用户配置的 `[sync]` 中，保留原来的 `format_version` 和机器条目：

```toml
[sync]
mode = "mirror"
respect_gitignore = true
exclude = ["scratch/"]
```

个人默认值也支持 `[run] wait = "10s"` 和 `[logs] tail = 100`。按显式参数、项目偏好、用户偏好、内置默认（`wait = "0s"`、`tail = 100`）的顺序生效，零值可以显式覆盖。可执行命令、setup 和环境变量仅属于项目，不从全局配置继承。

`run.wait` 只作用于提交。`ps <任务ID>` 仅在显式提供 `--wait` 时等待，列表查询不会等待任务结束。

同步策略按显式命令行参数、项目配置、全局默认、内置默认的顺序生效；内置默认是 `mirror` 和 `respect_gitignore = true`。标量逐项继承，排除项跨层追加，不替换内置保护规则。`respect_gitignore = false` 关闭 Git 跟踪及忽略选择，不关闭 Loaf 自身排除规则。`update` 保留远端已在本地删除或新被排除的旧文件，人类输出会提示这一点。JSON 包含生效的 `policy` 及选择、排除结果。

```bash
loaf sync --dry-run --mode update
loaf sync --respect-gitignore=false --exclude 'scratch/' --exclude '*.tmp'
loaf sync --json
```

## 多语言与结构化输出

帮助、交互提示、错误和检查结果原生支持英文与简体中文：

```bash
loaf --lang zh-Hans --help
LOAF_LANG=en loaf check --json
```

语言选择顺序为 `--lang`、`LOAF_LANG`、用户配置中的 `language`；默认 `auto` 使用系统语言。语言偏好不属于共享项目配置。

核心命令支持 `--json`，包含 `schema_version: 1`、稳定的 `ok` 字段，以及结构化数据或错误码。人类可读的 `message` 和 `hint` 可以随语言变化，命令名、字段和错误码保持不变；外部诊断保留原文。操作退出码：0 表示本次操作成功，2 表示参数或配置错误，3 表示提交未确认，1 表示其他失败。训练退出码在 `data.exit_code` 中，不作为 CLI 退出码；成功查询到失败训练时，仍是 `ok: true`、`data.status: "failed"`。未结束任务没有训练退出码，无法确认的结果如实报告未知。

## 开发

```bash
go run ./scripts/format.go    # Go 代码使用四空格格式化
bash scripts/check.sh        # 单元、静态、竞态、格式及漏洞检查
bash scripts/integration.sh  # 真实 SSH 与 Linux 进程测试，需要 Docker
bash scripts/integration.sh -run TestProjectRunWaitSetupAndRecovery
```

集成环境使用临时密钥和 CPU 脚本，验证发行包中的 macOS/宿主及 Linux 客户端原生 rsync、配置覆盖、镜像删除边界、中断恢复、并发启动保护、封装部署、跳板连接、断线执行、退出结果、重复提交、子进程停止、有界等待、setup 失败、日志取消及回执丢失恢复，不接触科研服务器。构建目标为 macOS/Linux 客户端及 Linux amd64、arm64 封装；交叉构建与实际运行分别验证。

Loaf 不负责租用机器、分配 GPU、自动修复环境或替代实验追踪工具。

## 许可证

[MIT](LICENSE)
