# Loaf

[English](README.md) | [简体中文](README.zh-Hans.md)

**本地写代码，使用已有的 SSH 科研机器。**

Loaf 将当前工作代码同步到已有 SSH 机器，运行断线后仍能继续的实验，并保存任务 ID、日志和退出结果。继续使用已有的 SSH 配置、密钥和跳板机，无须向另一套服务托管凭据。

> 预发布版本：[v0.1.0-rc.2](https://github.com/elebirds/loaf/releases/tag/v0.1.0-rc.2) 和源码均提供 `add`、`use`、`check`、`sync`、`run`、`ps`、`logs`、`stop`、`pull` 和 `version`。

## 安装

从 [Releases](https://github.com/elebirds/loaf/releases) 下载匹配平台的压缩包及校验文件。文件名中，`darwin` 表示 macOS，`linux` 表示 Linux，`arm64` 对应 Apple Silicon/ARM64，`amd64` 对应 Intel/AMD x86-64。将压缩包解压到新目录。

压缩包包含一个可执行文件、双语 README、Skill、示例、`BUILD.json` 及第三方许可证原文。解压后，在该目录执行：

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 loaf "$HOME/.local/bin/loaf"
"$HOME/.local/bin/loaf" version --json
```

按需将该目录加入 PATH。使用压缩包只需要本地 OpenSSH 和 rsync，不需要 Go。远端需要 Linux、rsync；实验的 Python 环境应单独准备。

构建目标为 macOS/Linux 的 amd64、arm64。macOS arm64、Linux arm64 客户端已运行完整 SSH 工作流，其他客户端目标仅经过交叉构建。Linux amd64 封装已执行真实 GPU 训练，Linux arm64 封装由 CPU fixture 验证。交叉构建不等于运行验证。

`BUILD.json` 保存源码提交、工作树修改状态、Go 版本、目标平台和依赖声明清单；`loaf version --json` 提供对应的构建标识，缺少来源信息时如实显示未知，不伪装成干净构建。将 `archive` 设为压缩包的准确文件名、`version` 设为文件名中的软件版本，再在包含压缩包和校验文件的目录中执行：

```bash
# archive 是压缩包的准确文件名
grep -F "  $archive" "loaf_${version}_checksums.txt" | shasum -a 256 -c -
```

公开发布的压缩包附带 GitHub 构建来源证明，可用 `gh attestation verify "$archive" --repo elebirds/loaf` 检查，详见 [GitHub 验证说明](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations#verifying-artifact-attestations-with-the-github-cli)。本地构建包含元数据和下载校验和，不含 GitHub 签名的来源证明。

### 从源码构建

需要 Go 1.27.3、Git、OpenSSH 和 rsync。Go 版本固定在 `mise.toml`；使用 mise 时，请先检查该文件再信任项目配置。

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

构建会内置 Linux amd64、arm64 远端封装，并生成 `dist/loaf`、平台压缩包和 `loaf_<version>_checksums.txt`。可用 `LOAF_BUILD_TARGETS` 选择其他构建目标，例如 `linux/amd64 linux/arm64 darwin/amd64 darwin/arm64`。

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

`add` 仅在本地保存连接，`check` 才会实际连接。JSON 和非交互终端命令不会询问密码或主机信任；请先用原生 SSH 建立主机信任。之后认证需要通过密钥、SSH agent 或原生 SSH 连接复用完成；没有连接复用的单次密码登录不会让后续命令自动获得认证。Loaf 不保存密码，也不关闭主机密钥验证。

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

Loaf 不自动安装 Python、uv，不猜测环境激活，也不上传数据集，请显式准备。数据和共享环境可放在 `cache/` 旁边，并通过绝对路径引用，因为执行目录是 `<remote_dir>/runs/<run-id>/work/`。提交时保存输出路径选择，之后可以用 `loaf pull` 取回。

可先运行[无依赖 CPU 示例](examples/cpu/)或[显式准备示例](examples/setup/)。Agent 可使用简短的 [Loaf Skill](skills/loaf/SKILL.md)，不需要读取内部文件、PID 或锁。

### 训练并使用一个小型 GPU 模型

[PyTorch 示例](examples/torch/)使用生成的数据训练分类器，无须下载数据，但需要已有的 CUDA/PyTorch 环境。进入示例目录，选择机器后按前述方式执行 `loaf run`、`loaf logs` 和 `loaf pull`。若非交互 SSH 的 PATH 中没有 `python3`，请在 `run.command` 或 `run --` 后显式指定已有解释器；Loaf 不读取猜测的 Shell 启动文件，也不自动激活环境。

输出包含 PyTorch checkpoint、指标、模型参数和留出推理样本。训练脚本会重新加载 checkpoint；取回结果后，将下载的任务目录作为参数运行 `python3 verify.py`，即可在本地独立使用模型参数推理。验证只需要 Python 标准库，不需要本地 PyTorch。GPU 示例由用户显式运行，不进入仅用 CPU 的 CI fixture。

## 停止实验

```bash
loaf stop                    # 当前项目最近一次提交的任务
loaf stop <任务ID> --json
```

停止始终使用任务保存的机器和位置，不随项目默认值改变。Loaf 请求所属监督进程终止受管进程组，等待确认，并保留日志和真实退出结果。setup 期间接收停止后，不再启动训练阶段；主动脱离受管进程组的进程不在清理范围内。

新任务从 TERM 到 KILL 默认留出 10 秒。可以在项目或用户配置中设置，也可以只覆盖本次请求：

```toml
[stop]
grace = "10s"
```

```bash
loaf stop <任务ID> --grace 30s
```

优先级为显式参数、项目设置、用户设置、封装默认值。宽限期支持 0s 到 5m，`0s` 允许立即升级为 KILL。首次接收的请求确定宽限期，重复请求不会重新计时或更改它；停止已结束任务会返回原结果。

除 SSH 连接时间外，确认最多等待宽限期加 5 秒，上限为 5 分钟。超时、确认丢失或客户端取消时，返回 `STOP_UNCONFIRMED` 和操作退出码 3；远端已接收的请求继续执行。请用 `loaf ps <任务ID> --json` 查询同一任务。监督进程身份失效时如实报告，不按可能复用的 PID 盲目发信号。已知接收策略时，JSON 包含 `data.stop_grace_millis`。

已有任务保留原封装及其默认宽限期。旧封装不支持显式宽限设置时，返回 `STOP_OPTIONS_UNSUPPORTED`（退出码 2）；移除 `--grace` 及项目或用户 `stop.grace` 设置后，可以使用其原默认策略。客户端不会替换仍在运行的监督进程。

## 取回实验输出

```bash
loaf pull                    # 当前项目最近一次提交的任务
loaf pull <任务ID> --json
loaf pull <任务ID> --path outputs --path checkpoints --to ./export
```

默认下载 `outputs/`，或该任务提交时保存的 `[outputs].paths`。之后修改项目输出配置或默认机器，不会改变已有任务的下载来源。重复指定 `--path` 可仅覆盖本次选择；路径是工作目录内的字面相对子路径，不展开 Shell 通配符。无须先检查或停止任务。

默认下载到 `<项目>/loaf-results/<run-id>/`，保留输出子路径。`--to` 指定准确的目的地，不再追加任务 ID；相对路径以当前目录为基准。根目录、主目录及包含项目源码的目录不能作为目的地。重复下载更新变更文件，不删除本地额外文件或远端输出。文件传输完成后才替换原文件，不原地覆盖；取消或失败后，对同一任务重新执行 pull 即可。取消下载不会停止训练。

项目和用户配置均支持以下独立偏好：

```toml
[pull]
base_dir = "loaf-results"
checksum = false
```

相对 `base_dir` 以项目根目录为基准，Loaf 在其下追加任务 ID。结果根目录应独立于源码，不能是项目根目录或其上级。配置在项目内的结果根目录会排除出源码上传，即使已被 Git 跟踪。临时 `--to` 若位于项目内且不应上传，请将其加入 `.gitignore` 或 `sync.exclude`。

默认按大小和修改时间判断文件变化。`--checksum` 可识别同大小、同时间戳的内容变化，代价是两端读取文件。按显式参数、项目偏好、用户偏好顺序覆盖；`--checksum=false` 可以关闭已启用的偏好，`--to` 覆盖默认目的地。

路径缺失时返回 `OUTPUT_MISSING`（退出码 1），存在的选定输出会尽可能下载。JSON 在错误旁保留 `data`，包含 `paths`、`downloaded_paths`、`missing_paths`、目的地及生效策略；只有 rsync 成功完成传输后才提供 `changed_files`。全部缺失不会被当成空下载成功。不安全路径或特殊文件会在传输前拒绝整组选择；相对链接的实际目标必须在选定输出范围内，本地已有链接也不能将写入导向结果目录之外。

运行中、失败、停止的任务都可以取回已有输出。运行中下载是当前内容的拷贝，不是完整 checkpoint 的一致快照；需要一致性时，应等待结束，或让训练脚本原子发布 checkpoint。支持的旧任务记录可由独立版本的读取封装访问，不替换原监督进程或改写记录。两个下载写入同一目的地时返回 `PULL_BUSY`；下载期间不占用远端源码同步锁或执行控制锁。

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
- Loaf 还会排除虚拟环境、常见缓存、`.git`、`.env`/`.env.*`、常见私钥名称、`*.pem`、`*.key`、`*.pt`、`*.pth`、`*.ckpt`、`*.safetensors` 和 `loaf-results/`，即使它们已被 Git 跟踪。配置的输出路径及当前本地结果根目录也被排除，默认输出是 `outputs/`。
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

核心命令支持 `--json`，包含 `schema_version: 1`、稳定的 `ok` 字段，以及结构化数据或错误码。人类可读的 `message` 和 `hint` 可以随语言变化，命令名、字段和错误码保持不变；外部诊断保留原文。操作退出码：0 表示本次操作成功，2 表示参数、配置错误或不支持的停止选项，3 表示提交或停止未确认，1 表示其他失败。训练退出码在 `data.exit_code` 中，不作为 CLI 退出码；成功查询到失败训练时，仍是 `ok: true`、`data.status: "failed"`。未结束任务没有训练退出码，无法确认的结果如实报告未知。

## 常见问题

源码缺失时，用 `loaf sync --dry-run` 检查 Git 和 Loaf 的排除规则；项目位于上级仓库的忽略目录中时，仍受该规则约束。命令失败时先查看 `loaf logs`：接管成功后仍可能因解释器、依赖或输入缺失而失败。提交或停止未确认时，应查询原 ID，而非另开替代任务。

## 开发

```bash
go run ./scripts/format.go    # Go 代码使用四空格格式化
bash scripts/check.sh        # 单元、静态、竞态、格式及漏洞检查
bash scripts/integration.sh  # 真实 SSH 与 Linux 进程测试，需要 Docker
bash scripts/integration.sh -run TestProjectRunWaitSetupAndRecovery
# 可选：验证旧版宿主客户端提交的任务
LOAF_PREVIOUS_BIN=/path/to/previous/loaf bash scripts/integration.sh -run TestPreviousRunnerStopCompatibility
# 可选：取回旧版客户端提交任务的输出
LOAF_PREVIOUS_PULL_BIN=/path/to/previous/loaf bash scripts/integration.sh -run TestPreviousRunnerPullCompatibility
```

集成环境使用临时密钥和 CPU 脚本，验证发行包中的 macOS/宿主及 Linux 客户端原生 rsync、配置覆盖、镜像删除边界、中断恢复、并发启动保护、封装部署、跳板连接、断线执行、退出结果、重复提交、子进程停止、有界等待、setup 失败、日志取消、回执丢失恢复和增量输出下载。下载场景覆盖缺失/不安全路径、磁盘满、取消、重试、目的地隔离及旧任务兼容，不接触科研服务器。构建目标为 macOS/Linux 客户端及 Linux amd64、arm64 封装；交叉构建与实际运行分别验证。

Loaf 不负责租用机器、分配 GPU、自动修复环境或替代实验追踪工具。

维护者显式推送 `vX.Y.Z` 或预发布标签后，发布工作流才会运行同一套 CI、SSH 检查和 `scripts/build.sh`，为压缩包生成来源证明并创建 Release。普通分支和 PR 没有发布权限。

## 许可证

[MIT](LICENSE)
