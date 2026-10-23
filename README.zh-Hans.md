# Loaf

[English](README.md) | [简体中文](README.zh-Hans.md)

**本地写代码，使用已有的 SSH 科研机器。**

Loaf 将当前工作代码同步到已有 SSH 机器，运行断线后仍能继续的实验，并保存任务 ID、日志和退出结果。继续使用已有的 SSH 配置、密钥和跳板机，无须向另一套服务托管凭据。

> 当前源码目标版本为 v0.4.0-rc.2 候选版，提供离线任务查看、名称／标签、SSH 诊断及显式日志游标续接，并保留命令级云 SSH 复用。已发布的 [v0.1.0-rc.2 压缩包](https://github.com/elebirds/loaf/releases/tag/v0.1.0-rc.2) 提供核心实验闭环；使用新命令请从当前源码构建。

## 安装

从 [Releases](https://github.com/elebirds/loaf/releases) 下载匹配平台的压缩包及校验文件。文件名中，`darwin` 表示 macOS，`linux` 表示 Linux，`arm64` 对应 Apple Silicon/ARM64，`amd64` 对应 Intel/AMD x86-64。将压缩包解压到新目录。

压缩包包含一个可执行文件、双语 README、Skill、示例、`BUILD.json` 及第三方许可证原文。解压后，在该目录执行：

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 loaf "$HOME/.local/bin/loaf"
"$HOME/.local/bin/loaf" version --json
```

按需将该目录加入 PATH。使用压缩包只需要本地 OpenSSH 和 rsync，不需要 Go。远端需要 Linux、rsync；实验的 Python 环境应单独准备。

构建目标为 macOS/Linux 的 amd64、arm64。macOS arm64、Linux arm64 客户端已在本地运行完整 SSH 工作流，Linux amd64 客户端由 GitHub CI 实际验证；macOS amd64 客户端仅经过交叉构建。Linux amd64 封装已执行真实 GPU 训练，Linux arm64 封装由 CPU fixture 验证。交叉构建不等于运行验证。

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

构建会内置 Linux amd64、arm64 远端封装，并生成 `dist/loaf`、平台压缩包和 `loaf_<version>_checksums.txt`。默认构建标识由 `VERSION` 中的目标版本加开发提交和时间戳组成；显式设置 `VERSION` 环境变量可选择准确的软件版本。可用 `LOAF_BUILD_TARGETS` 选择其他构建目标，例如 `linux/amd64 linux/arm64 darwin/amd64 darwin/arm64`。

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

用 `loaf targets` 列出已保存机器，`loaf targets lab` 查看一项，`loaf remove lab` 移除别名。这些命令只读写本地设置，不连接远端。移除别名不删除历史任务或远端数据；仍选择该别名的项目需要显式重新选机，不改写项目 TOML，也不暗中选择其他机器。

`add` 仅在本地保存连接，`check` 才会实际连接。JSON 和非交互终端命令不会询问密码或主机信任；请先用原生 SSH 建立主机信任。这些静态机器之后的认证需要通过密钥、SSH agent 或原生 SSH 连接复用完成；没有连接复用的单次密码登录不会让后续命令自动获得认证。Loaf 不保存密码，也不关闭主机密钥验证。

需要新租用 AutoDL 实例时，请看[租用并接入 AutoDL 算力](#租用并接入-autodl-算力)。

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

`ps` 默认查询当前项目最近 20 个已提交或提交未确认任务，可用 `--limit` 或 `[ps].limit` 调整数量上限；指定 ID 时只查询该任务。部分查询失败会保留其他成功结果，不会把训练标成失败。`logs` 省略 ID 时选择最近一次已提交或提交未确认任务，不会退回更旧任务；明确的检查、同步失败不替换最近任务。已有任务保留提交时的连接及远端位置，不随项目默认值改变，指定 ID 的查询不能用 `--target` 改向。

需要 runner 的操作必须与本地 CLI 同版本。Loaf 自动部署并验证内置封装，缺失或版本不符时会修复；不下载任意版本，也不热替换正在运行的监督进程。其他版本创建的任务返回 `VERSION_MISMATCH`；升级时请并存保留对应 CLI，并显式使用它操作原有任务。未知记录格式会直接拒绝，不改写数据。

### 寻找任务与离线查看

提交时为任务指定便于识别的名称和可重复标签：

```bash
loaf run --name "ablation-seed1" --tag ablation --tag seed1 -- python3 train.py
loaf ps --name "ablation-seed1" --tag seed1 --json
loaf ps --local --tag ablation --json
loaf inspect <任务ID> --json
```

名称和标签在提交时保存于本地，之后不可替换。名称不必唯一，任务操作仍以 ID 为身份。名称／标签按精确值在有界查询前筛选，重复标签要求全部匹配；不改变命令、远端环境、路径或最近任务选择。

`ps --local`（含指定 ID）与 `inspect <任务ID>` 只读取本地资料，不启动 SSH、不查询云平台、不访问凭据或部署封装，也可查看旧版本任务和已删除别名。JSON 标记 `source: "local"`；除已有清理收据保留的终态外，执行状态为 `unknown`。本地模式拒绝实时 `--status` 筛选及非零 `--wait`。

`inspect` 包含保存的 `version`、当前 `client_version`、`compatible`、冻结的机器／实例身份、提交确认、位置和输出路径，不暴露 SSH 参数、凭据引用或私有执行环境。保存任务的版本错误还提供 `error.expected_version`、`error.actual_version`。旧可执行文件可按版本命名保留，例如 `loaf-v0.4.0-rc.1`，不要覆盖管理运行中任务所需的唯一 CLI。这些命令不下载 CLI，也不迁移任务。

### 筛选任务历史

```bash
loaf ps --status failed --status stopped --limit 50 --json
loaf ps --since 2026-10-01T00:00:00Z --target lab --json
```

`--since`、`--until` 接受 RFC3339 时间，筛选保存的提交尝试时间，而非结束时间。列表的 `--target` 按原绑定别名筛选，包括已经移除的别名。先按时间、别名选择，再进行有界远端查询；状态筛选只作用于这些观察，不会无限往前搜索以填满数量上限。JSON 显示候选、已查、匹配、未确认及未检查范围；查询失败的行即使不匹配状态也会保留 `query_error`，不会伪装成没有任务。指定单个 ID 时不能叠加列表筛选。

### 短任务：有界等待结果

```bash
loaf run --wait 10s --json -- python3 smoke.py
loaf ps <任务ID> --wait 30s --json
```

从远端确认接管后计算等待预算：窗口内结束时，一次返回终态、退出结果和有限日志；尚未结束时，返回已观察到的状态和原任务 ID，任务继续运行。`--wait 0s` 在接管后返回，最长可等待 5 分钟。这是观察预算，不是训练时限；取消观察不会停止已接管任务。JSON 在确认接管后保留 `submission: "submitted"`，即使之后观察失败、返回 `status: "unknown"` 和错误；`submission: "uncertain"` 则表示仍需查询同一 ID 确认接管。

普通模式显示准备阶段，并在 `run --wait` 期间持续显示日志。JSON 模式只返回一份有限结果，不混入进度文字。持续日志使用 NDJSON：

```bash
loaf logs <任务ID> --tail 100 --json
loaf logs <任务ID> -f --json
```

日志默认取最后 100 行，日志文本最多 256 KiB，`log.truncated` 表示省略了输出。持续读取产生 `data.type = "chunk"`，结束时为 `"end"`，任务查询结果在 `data.run` 中；流错误为 `"error"`，带普通错误对象。偏移量按原日志字节计算。程序输出不翻译、不解析成指标；及时看到日志还需要程序主动刷新输出。

若实时日志需要承受一次临时断线，可显式启用续接：

```bash
loaf logs <任务ID> -f --reconnect
loaf logs <任务ID> -f --resume "$cursor" --json
```

有限日志查询通过 `data.log.cursor` 返回游标，流式 chunk 使用 `data.run.log.cursor`。应在消费完该块后保存游标。`--resume` 须配合 `--follow`，不能与显式 `--tail` 同用，并从已确认的原始日志字节偏移续读；与 `--reconnect` 一样，最多允许一次临时断线重连。默认 `logs -f` 仍保持单连接行为。

游标绑定软件版本、任务、位置与日志文件，并校验偏移边界内容。文件被替换、截断或边界内容被改写时返回 `LOG_CHANGED`，不会悄悄从尾部重新开始；此时须显式读取新尾部以获取新游标。输出消费失败、取消、认证／信任失败或已交付 end 事件都不触发重启。已接管任务独立继续运行。

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

## 检查机器与转发服务

```bash
loaf exec -- nvidia-smi
loaf ssh
loaf forward 6006 --local-port 16006
```

`exec` 是前台机器命令：及时显示 stdout/stderr，结果包含远端退出码。默认在远端用户主目录执行，`--cwd` 可以指定其他目录，相对路径从该主目录解析。它不同步源码、不运行 setup、不生成任务 ID，也不承诺断线后的持久执行。需要这些能力的实验应使用 `run`，短实验可加 `--wait`。`exec --` 后的参数不隐式经过 Shell；需要 Shell 语法时显式调用 Shell。

`exec --json` 输出有界 NDJSON 数据块及最终结果或错误。数据块区分 stdout/stderr，以文本或 base64 保留原始字节。确认的远端非零退出返回 `EXEC_FAILED`（操作退出码 1）；SSH 返回 255 或取消观察时，保守返回 `EXEC_UNCONFIRMED`（退出码 3）。结果未确认时不要自动重放命令。

`ssh` 打开所选机器的原生交互终端，不是进入已有实验。`forward` 使用原生 OpenSSH，将远端已经运行的回环服务转到本地回环端口；省略 `--local-port` 时两端端口相同。示例通过本地 `http://127.0.0.1:16006` 访问远端 6006。它不自动启动 TensorBoard/Jupyter，转发端口在监听也不等于应用已经健康。

转发以前台独立连接运行，关闭 SSH 后台化和连接复用，退出只撤销本次转发，不关闭用户其他 master 或停止训练。密钥、agent、跳板及主机密钥检查仍生效；非交互的独立连接不能借用另一个仅密码认证 master 的认证状态。交互 `ssh` 和 `forward` 明确拒绝 `--json`。

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

路径缺失时返回 `OUTPUT_MISSING`（退出码 1），存在的选定输出会尽可能下载。JSON 在错误旁保留 `data`，包含 `paths`、`downloaded_paths`、`missing_paths`、目的地及生效策略；只有 rsync 首次传输成功后才提供 `changed_files`，经过恢复重试时不报告只覆盖最后一次尝试的变更计数。全部缺失不会被当成空下载成功。不安全路径或特殊文件会在传输前拒绝整组选择；相对链接的实际目标必须在选定输出范围内，本地已有链接也不能将写入导向结果目录之外。

运行中、失败、停止的任务都可以取回已有输出。运行中下载是当前内容的拷贝，不是完整 checkpoint 的一致快照；需要一致性时，应等待结束，或让训练脚本原子发布 checkpoint。两个下载写入同一目的地时返回 `PULL_BUSY`；下载通过独立数据锁与清理互斥，不占用远端源码同步锁或执行控制锁。

## 回收已结束任务的数据

```bash
loaf clean <run-id> --dry-run --json
loaf clean <run-id>
loaf clean <run-id> --yes --json
```

清理必须提供明确 ID，不隐式选择最新或全部任务。预览显示原绑定机器及将删除的数据，不部署新封装或删除文件。实际清理时会重新检查归属、终态和锁，预览不等于已经保留了删除许可。普通模式询问一次确认，JSON 或非交互删除需要 `--yes`。

清理会删除所选任务的远端工作文件、日志和输出；需要保留的内容请先取回。本地已下载结果不受影响，但删除的远端数据无法撤销。运行中、提交未确认、归属不明或结果未确认的任务会被拒绝，不隐式先停止。其他任务、源码缓存、共享数据和环境不在清理范围。

保留少量删除回执与 ID 声明，防止延迟到达的请求再次执行相同任务。`ps` 保留原执行结果并标记清理信息；`logs`、`pull` 和 `stop` 返回 `RUN_CLEANED`。清理最新任务不会让默认命令悄悄退回旧任务。部分失败或确认丢失时，查看报告的 ID，仅对这些明确的原 ID 重试。

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

当前任务索引使用格式 2。名称／标签另存于 `run-metadata/` 下的私有格式 1 文件，不改变定位器结构，对应旧 CLI 仍可读取其任务。更早的索引格式会被拒绝，不自动迁移或覆盖；不同索引格式的客户端应使用独立 `XDG_STATE_HOME`。

个人同步默认值放在已有用户配置的 `[sync]` 中，保留原来的 `format_version` 和机器条目：

```toml
[sync]
mode = "mirror"
respect_gitignore = true
exclude = ["scratch/"]
```

个人默认值也支持 `[run] wait = "10s"` 和 `[logs] tail = 100`。按显式参数、项目偏好、用户偏好、内置默认（`wait = "0s"`、`tail = 100`）的顺序生效，零值可以显式覆盖。可执行命令、setup 和环境变量仅属于项目，不从全局配置继承。

项目与用户设置还支持 `[ps] limit = 20`、`[exec] cwd = ""`。列表数量接受 1–1000，显式零值是错误，不会回退默认。exec 目录的空字符串可显式恢复远端主目录，即使较低优先级配置选择了其他目录。两项均按命令行、项目、用户、内置默认的顺序生效。

`run.wait` 只作用于提交。`ps <任务ID>` 仅在显式提供 `--wait` 时等待，列表查询不会等待任务结束。

同步策略按显式命令行参数、项目配置、全局默认、内置默认的顺序生效；内置默认是 `mirror` 和 `respect_gitignore = true`。标量逐项继承，排除项跨层追加，不替换内置保护规则。`respect_gitignore = false` 关闭 Git 跟踪及忽略选择，不关闭 Loaf 自身排除规则。`update` 保留远端已在本地删除或新被排除的旧文件，人类输出会提示这一点。JSON 包含生效的 `policy` 及选择、排除结果。

```bash
loaf sync --dry-run --mode update
loaf sync --respect-gitignore=false --exclude 'scratch/' --exclude '*.tmp'
loaf sync --json
```

## 租用并接入 AutoDL 算力

已有静态 SSH 机器不需要配置云账号。AutoDL 可用主账号手机号或完整的 `子用户名@主账号ID` 登录；同一已验证会话按账号权限访问**普通实例**（`standard`）和 **Pro**（`pro`）。密码输入不回显，不提供接收密码值的命令行参数。

```bash
loaf cloud login autodl-main --username 'your-subuser@master-id' --default
loaf cloud list --account autodl-main
loaf cloud list --account autodl-main --product pro
```

### 选择凭据保存方式

```bash
loaf cloud login autodl-main --credential-store keyring   # 默认
loaf cloud login autodl-main --credential-store file      # 未加密的私有文件
```

`keyring` 使用 macOS 钥匙串或 Linux Secret Service（需要可用的用户会话）。`file` 不依赖凭据库服务：保存在 `$XDG_STATE_HOME/loaf/credentials/`，默认 `~/.local/state/loaf/credentials/`，目录权限 `0700`、文件权限 `0600`。文件模式能隔离其他系统用户，不能防止以你身份运行的软件读取。Keyring 失败时不暗中改写文件。CLI 当前支持 macOS/Linux；Windows 暂需通过 WSL 使用。

在用户配置中为**新登录**设置默认值，不放入项目 `loaf.toml`：

```toml
[cloud]
credential_store = "file"    # 或 "keyring"
```

已有账号保持保存的方式，只有显式 `--credential-store` 才切换。切换方式或替换账号时，会保留历史任务引用的旧凭据条目，不重写其访问路径。`cloud logout <名称>` 只删除当前选定条目；`cloud account remove <名称>` 只移除别名及默认选择，不删除凭据或云资源。若需让所有副本失效，应在平台撤销会话。

默认保存会话令牌和 AutoDL 可重用的 SHA-1 密码摘要，不保存原始密码。**该摘要等同于登录凭据**，文件模式也一样；`--remember-password=false` 则只保存会话。会话过期后，`cloud login autodl-main` 用已保存摘要重新认证，`--prompt` 强制重新输入密码。验证到不同身份时必须加 `--replace`，并生成新的凭据引用；认证失败不会替换已有登录。

主账号遇到短信／人机验证时返回 `AUTH_CHALLENGE_REQUIRED`，需通过 AutoDL 完成验证；已有短信码可使用 `cloud login autodl-main --verify` 输入。Loaf 不解验证码、不自动重放密码请求。非交互调用可显式使用 `--username ... --password-stdin`；`--json` 不弹出提示。不要把密码写进 Shell 历史或聊天。

自动化仍可显式引用环境变量中的令牌：

```bash
loaf cloud account add autodl-pro --product pro --auth developer \
  --token-env AUTODL_PRO_TOKEN
loaf cloud account add autodl-session --product standard --auth session \
  --token-env AUTODL_WEB_TOKEN
loaf cloud account list --json
```

令牌从调用环境读取，TOML 仅保存变量名。开发者令牌走官方 Pro 控制面，不能访问普通实例；网页会话走网页控制面，原生登录保存并核验主／子账号身份及权限。`--app-version` 可显式设置网页控制台请求头，不抓取浏览器令牌、不自动创建开发者令牌。

`cloud login --product pro` 设置账号默认产品。每次操作按显式 `--product`、资源模板 `product`、账号默认值选择；已绑定别名固定产品，拒绝冲突覆盖。选择产品不隐式切换认证方式、接口主机或凭据。

### 接入已有实例

```bash
loaf cloud credentials <实例ID> --account autodl-main
loaf cloud bind gpu-a --instance <实例ID> --account autodl-main
loaf use gpu-a
loaf run -- python3 train.py
```

`credentials` 显示当前 SSH 地址，默认隐藏密码；只有显式 `--show-password` 才会显示密码。首次主机信任通过原生 SSH 确认，非交互命令不会批准新主机或变化的主机密钥。云连接优先使用密钥，也能通过 OpenSSH 原生 askpass 使用临时获取的密码。密码只在内存中，经私有的临时本地套接字传递，不写入配置、子进程参数或环境变量；临时启动脚本不含密码。

云 runner 操作为每个绑定实例及连接配置使用本次命令私有的 OpenSSH 主连接；连接健康期间只解析一次连接信息，探测、封装请求和原生 rsync 共用该认证连接。Loaf 不借用已有主连接，也不改写 SSH 配置。正常退出和取消只关闭本命令创建的连接；父进程生命周期管道保证 CLI 被强制结束后也不会留下持久连接。静态 SSH 机器及前台 `exec`、`ssh`、`forward` 保留原有连接行为。

`bind` 先检查账号能读取该实例，再保存绑定并验证 SSH 认证。`ready` 仅表示平台运行、SSH 认证成功且机器已保存，不表示 Python/CUDA 环境已就绪。`--ready-timeout` 默认 2m，0s 表示仅做一次有界检查。实例已创建但连不上时可能已经计费，请保留 ID 并修复连接，不要重新租卡。可用 `loaf cloud bind gpu-a --request <请求ID>` 恢复同一租用结果，不会创建替代实例。显式 `--replace` 可重绑别名，但历史任务仍刷新其原实例的连接信息。

### 在配置或命令行中设置条件

可选的项目 `loaf.toml` 模板只描述实验资源需求，不包含凭据：

```toml
[resources.train]
gpu_vendor = "nvidia"
gpu_count = 1
min_gpu_memory_gib = 24       # 每张卡的显存，不是多卡总和
min_memory_gib = 32
max_compute_hourly_price = "2.50"
currency = "CNY"
wait_timeout = "30m"
poll_interval = "10s"

[resources.train.autodl]
image = "your-image-uuid"
```

普通实例与网页会话 Pro 均读取平台的价格预览 API。预算采用精确十进制 CNY 和**整实例每小时计算费用**，单独报价的存储按日列示；不从原始库存价格猜单位，也不要求用户声明单位。创建前再核价一次。小时单价上限不是总费用上限，也不代表平台锁价；报价与分配之间平台仍可能变化。

```bash
loaf cloud search --account autodl-main --profile train
loaf cloud watch --account autodl-main --profile train
loaf cloud acquire gpu-a --account autodl-main --profile train --yes
loaf use gpu-a
loaf run -- python3 train.py
loaf logs
loaf pull
```

不需要预先创建模板，也可以直接传入条件：

```bash
loaf cloud acquire gpu-a --account autodl-main \
  --gpu-count 1 --min-gpu-memory 24GiB --min-memory 32GiB \
  --max-compute-hourly-price 2.50 --currency CNY \
  --image your-image-uuid --wait 30m --yes
```

其他条件包括 `--gpu-vendor`、`--gpu-model`、`--min-cpus`、`--min-disk` 和 `--region`。型号和地区使用平台的准确名称/ID，先查看查询结果，不要猜测。内存、显存、磁盘下限必须带 `GiB` 单位。普通实例数据盘扩容使用 `--expand-data-disk`，Pro 系统盘扩容使用 `--expand-system-disk`，两者按平台的 GB 约定传入。租用前不验证镜像是否能拉取或其中依赖是否就绪。镜像按产品区分：`--image` 指定普通实例基础镜像 UUID 或 Pro 镜像 UUID；两种网页会话产品的私有镜像改用 `--private-image`，二者不能同时设置；开发者 Pro 使用其独立的镜像 UUID。

每个字段独立按命令行、项目模板、用户模板、默认值覆盖。`resources.default` 提供可选公共默认值，命名模板在其上覆盖；用户 `[cloud.defaults]` 提供更低优先级的个人默认值。型号和地区列表整体替换，不合并扩大范围；合法的显式零值、空值保留。账号及 `[cloud]` 中的 `default_account` 仅属于用户配置，项目文件不能设置。

用户配置中的独立安全上限不能被项目或命令行提高、清空：

```toml
[cloud.safety]
max_compute_hourly_price = "3.00"
currency = "CNY"
```

### 换卡时保留原环境和数据

已有机器没有空闲 GPU 时，可以持续等待**同地区**的可用机器并克隆：

```bash
loaf cloud acquire gpu-copy --account autodl-main --product standard \
  --clone-from old-gpu --gpu-model 'RTX 5090' --gpu-count 1 \
  --max-compute-hourly-price 3.00 --wait 24h --ready-timeout 10m --yes
```

`--clone-from` 接受实例 ID 或已绑定的云机器别名。支持使用主／子账号会话的 AutoDL 普通实例，暂不支持 Pro／开发者令牌克隆。源实例必须已关机且未在克隆中；Loaf 保留源实例，不会自动停止它。

沿用现有抢占循环等待匹配库存、复查候选和价格，随后申请**复制系统盘和数据盘**。不会回退到其他地区或空白新建，冲突的地区配置会报错。候选机器还须满足源镜像的厂商、架构及 CUDA 要求。克隆继承源镜像，忽略配置中的镜像默认值；显式镜像、规格、CUDA、系统盘参数和 `--allow-unquoted` 会被拒绝。

数据盘默认继承源实例的扩容量；项目／用户配置及 `--expand-data-disk` 可以增大，不能缩小。计算条件与安全上限仍按原有优先级生效。没有等待偏好时，克隆默认抢占 30m；项目／用户配置或 `--wait` 可以覆盖，包括用显式 `0s` 只尝试一次。单次窗口最长 24h，持续抢占期间需要保持命令运行。

SSH 能连上不等于磁盘复制完成。克隆的 `ready` 还要求平台确认数据盘拷贝成功。观察超时时保留返回的请求／实例 ID，继续接入同一克隆结果：

```bash
loaf cloud info gpu-copy --json
loaf cloud bind gpu-copy --request REQUEST_ID --ready-timeout 10m
```

平台提供拷贝状态时，`clone_data_status` 返回 `copying`、`succeeded`、`failed` 或 `unknown`。`CLONE_FAILED` 会保留实例以便恢复；观察超时或拷贝失败均不会另租替代机器。请求记录固定源实例身份和地区，重启进程或更改机器／账号别名不会重定向它们。

### 监测与付费分配分开

| 操作 | 行为 |
| --- | --- |
| `cloud search` | 一次有界只读库存查询；必要字段未知时不匹配，符合条件的资源按价格排序。 |
| `cloud watch` | 前台只读监测，必须通过 `--wait` 或模板设置正等待时间；不租用实例。 |
| `cloud acquire <别名>` | 显式授权最多租用一台；重新检查条件、创建前落盘请求意图，随后接入。 |

默认轮询周期 10s，可配置 10s–10m 的最小间隔，创建前另做一次即时复查；临时故障增加退避、抖动，并遵守 `Retry-After`。等待最长 24h，关闭客户端结束监测，但不撤销已创建的实例。人类输出显示匹配数、拒绝原因、查询覆盖及观察时间；有限命令和 acquire 的 JSON 只返回一个结果，`watch --json` 使用 NDJSON 变化事件、低频心跳和结束/错误结果。运行期间修改配置不会扩大已授权范围。

普通实例库存来自**非官方网页会话 API**。硬件单位依据平台公开租用表单归一化：显存按单卡、CPU/内存按卡数计算分配量；离线或实例槽位耗尽的机器不匹配，缺少必要字段时保留未知。每次最多检查 `--limit` 条，默认 100、上限 1000；分页未完整读取会明确显示，“最便宜”只针对已检查集合。

网页会话 Pro 可通过 GPU 目录、数字规格 ID 和本次请求报价进行 `search`、`watch`：

```bash
loaf cloud search --account autodl-main --product pro --region westDC3
loaf cloud acquire pro-a --account autodl-main --product pro \
  --gpu-model 'platform-model-name' --region westDC3 \
  --image your-base-image-uuid --cuda-from 118 \
  --max-compute-hourly-price 3.00 --wait 10m --yes
```

网页 Pro 未指定 `--region` 时，分别查询控制台支持的 `westDC3` 和 `beijingDC2` 数据中心。可选 `--gpu-spec-id` 固定网页数字规格，否则按条件选取；它不同于开发者 API 的 `--gpu-spec`（UUID）。目录库存是型号／地区的汇总可用量，不是某规格的预留。CPU／内存区间仅采用保守下限，不承诺最终分配量；目录未核验的字段（包括显存、厂商）保留未知，不能满足相应条件。创建前复查所选地区、型号及规格，但仍可能遇到无库存；不把弹性算力库存当成 Pro 库存。

Pro 开发者 API 已接入创建、列表、详情、开关机及连接，但此适配器尚无已验证的只读库存和租前报价能力，`search`、`watch` 返回 `UNSUPPORTED_CAPABILITY`。可以显式指定 Pro 规格，使用**已授权的分配尝试**；仅明确映射的“无库存”拒绝可在授权范围内重试：

```bash
loaf cloud acquire pro-a --account autodl-pro \
  --gpu-spec your-spec-uuid --image your-image-uuid --cuda-from 118 \
  --gpu-count 1 --wait 10m --allow-unquoted --yes
```

这不是只读库存监测。`--allow-unquoted` 不能绕过有效的价格上限，此时直接返回 `PRICE_UNVERIFIABLE`，不付费；无法核验的通用硬件下限也会拒绝，不暗中放宽。Pro 地区为数据中心 ID，与普通实例的地区标识不同。`118` 是平台的 CUDA 11.8 最低版本代码；Loaf 不通过平台启动命令执行实验。接口依据见 [Pro 官方 API](https://www.autodl.com/docs/instance_pro_api/)、[当前普通实例租用表单](https://www.autodl.com/assets/index.9040cb9d.js) 及 [GET 查询参数实测记录](https://github.com/RordChang/autodl-pro-codex-skill/blob/main/references/api.md)。开发者 Pro snapshot/status 使用查询参数，不自动改为 GET body，也不重放变更请求。

### 核对不确定结果，明确控制计费

```bash
loaf cloud requests --json
loaf cloud requests <请求ID> --json
loaf cloud reconcile <请求ID> --instance <已知实例ID> --json
loaf cloud bind gpu-a --request <请求ID>
loaf cloud start gpu-a --wait 2m --yes
loaf cloud stop gpu-a --wait 2m --yes
```

操作凭据以私有 JSON 保存在任务索引旁的 `cloud/` 中。创建中的网络中断、取消、无效响应或结果不明的服务器错误返回 `CLOUD_UNCONFIRMED`（退出码 3），保留请求 ID 及已知实例 ID；不重放该请求、不自动租用替代实例。未确认的分配意图会阻止此账号继续租用；只有明确映射的无库存拒绝，才会在原授权时间范围内进行下一次串行尝试。

创建结果不明时，先核对平台控制台，再关联已知实例 ID。如果人类已确认**没有创建实例**，可用 `cloud reconcile <请求ID> --no-instance --yes` 解除保护，不能从一次有界列表自动推断未创建。开关机操作凭据通过查询原实例核对，reconcile 不重复发送开关机请求。

`cloud start/stop` 操作**整个实例**，而 `loaf stop` 只停止一个实验。平台接受开关机请求不代表完成，通过 `--wait` 或 `info` 观察；关机可能影响其他用户或任务，存储仍可能计费。Loaf 不释放实例、不在训练后自动关机，也不实施运行时长或总费用限制；租用不自动启动训练，`run` 不隐式租卡。

## 多语言与结构化输出

帮助、交互提示、错误和检查结果原生支持英文与简体中文：

```bash
loaf --lang zh-Hans --help
LOAF_LANG=en loaf check --json
```

语言选择顺序为 `--lang`、`LOAF_LANG`、用户配置中的 `language`；默认 `auto` 使用系统语言。语言偏好不属于共享项目配置。

核心命令支持 `--json`，包含 `schema_version: 1`、稳定的 `ok` 字段，以及结构化数据或错误码。人类可读的 `message` 和 `hint` 可以随语言变化，命令名、字段和错误码保持不变；外部诊断保留原文。操作退出码：0 表示本次操作成功，2 表示参数或配置错误，3 表示提交、停止、前台执行、清理或云变更未确认，1 表示其他失败。训练退出码在 `data.exit_code` 中，不作为 CLI 退出码；成功查询到失败训练时，仍是 `ok: true`、`data.status: "failed"`。未结束任务没有训练退出码，无法确认的结果如实报告未知。

## 常见问题

```bash
loaf check --trace --json
loaf pull <任务ID> --trace --json
```

`--trace` 为有限 JSON 结果增加有界 `trace` 对象，提供阶段耗时、连接解析、成功建立的私有主连接、重连和只读重试计数。重试事件给出 `private_master_exit` 等稳定原因；操作恢复成功后仍保留首次失败的诊断。普通模式将诊断写入 stderr。收集须显式启用，不记录命令、端点、请求体、密码、token 或环境变量值。

耗时可能嵌套，不应相加。私有主连接计数适用于云 runner 连接，不代表用户静态 SSH 的连接共享；诊断涵盖 SSH 工作流及其云连接查询，不追踪无关的云目录／租用 HTTP 操作。明细最多 512 条，以 `truncated: true` 标记截断，计数仍完整。`check --trace` 保持远端只读，除非另外显式指定 `--runner`。持续流、前台 `exec`、交互终端、隧道及云 watch 不支持 trace。

连接失败会提供稳定的 `error.stage`，例如 `cloud_connection`、`ssh_connect`、`runner_verify`、`sync`、`launch`、`observe`、`pull`，并保留原错误码及可用的 SSH／平台诊断。封装版本验证失败本身不代表可执行文件损坏。

有限只读探测、状态／日志查询、输出检查和下载，在识别到临时传输故障后最多追加一次尝试，退避短暂且可取消。认证、主机密钥、配置和路径错误不重试；默认日志流已输出部分内容后不重启；只有显式 `logs -f --reconnect` 或 `--resume` 才启用游标恢复。源码同步、封装上传、提交、停止、清理和前台命令不自动重放。下载保留已完成文件，并沿用原任务及目的地。

源码缺失时，用 `loaf sync --dry-run` 检查 Git 和 Loaf 的排除规则；项目位于上级仓库的忽略目录中时，仍受该规则约束。命令失败时先查看 `loaf logs`：接管成功后仍可能因解释器、依赖或输入缺失而失败。提交或停止未确认时，应查询原 ID，而非另开替代任务。

## 开发

```bash
go run ./scripts/format.go    # Go 代码使用四空格格式化
bash scripts/check.sh        # 单元、静态、竞态、格式及漏洞检查
bash scripts/integration.sh  # SSH、Linux 进程与凭据测试，需要 Docker
bash scripts/integration.sh -run TestProjectRunWaitSetupAndRecovery
bash scripts/integration.sh -run TestLinuxCloudCredentialStores
```

集成环境使用临时密钥和 CPU 脚本，验证发行包中的 macOS/宿主及 Linux 客户端原生 rsync、镜像边界、中断同步恢复、并发启动、同版本封装部署、跳板连接、断线执行、退出结果、进程组停止、有界等待、日志取消和增量下载。前台操作覆盖即时输出、字面 argv、PTY 登录、独立隧道和取消；历史及清理覆盖原目标绑定、明确删除范围、传输锁、重试和终态保留，不接触科研服务器。构建目标为 macOS/Linux 客户端及 Linux amd64、arm64 封装；交叉构建与实际运行分别验证。

云测试通过模拟 API 验证两种 AutoDL 产品，并使用隔离 SSH 服务器验证原生密码认证、未知主机密钥拒绝、固定实例绑定及结果下载。跨进程测试覆盖会话过期、保存摘要后的重新认证、密码变更、登出后重新登录、凭据后端选择，以及响应丢失或客户端被杀后的恢复，确保不会重复租用或重放开关机请求。

Linux 凭据 fixture 在容器私有会话中以普通用户运行 Secret Service 钥匙环，验证原生读写、从文件切换至钥匙环的登录生命周期，以及会话总线不可用的情况；不会挂载宿主钥匙环。平台 HTTP 仍由模拟服务提供，这些测试不能证明真实账号的接口可用性、当前库存或实际计费；真实租用验收需要单独授权账号及预算。

Loaf 不调度 GPU 任务、不自动修复环境，也不替代实验追踪工具。云租用必须显式触发，不提供队列、常驻服务、自动关机或隐式租卡。

维护者显式推送 `vX.Y.Z` 或预发布标签后，发布工作流才会运行同一套 CI、SSH 检查和 `scripts/build.sh`，为压缩包生成来源证明并创建 Release。普通分支和 PR 没有发布权限。

## 许可证

[MIT](LICENSE)
