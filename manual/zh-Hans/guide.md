# 运行并取回实验

[首页](../../README.zh-Hans.md) · [命令参考](commands.md) · [通知与队列](async.md) · [English](../en/guide.md)

## 使用概览或直接命令

在终端运行 `loaf -i`，可打开当前项目的本地概览：运行项目、浏览任务与日志、
收集结果、管理机器或进入云资源。「更多」包含源码同步、队列、通知和事件。
参数完整的调用保持直接执行，无须先经过菜单。

```bash
loaf --help
loaf help run --json
loaf run --wait 10s --json -- python3 train.py
```

不加 `-i / --interactive` 时，即使有终端也只返回帮助、有限结果或缺参错误。
菜单与缺项向导须显式开启；该选项需要终端输入，不能与 `--json` 合用。
帮助和版本保持离线；原生 SSH／exec 流及显式认证提示保留各自约定。
交互模式不等于批准付费或删除，显示的下一步也不授予执行权限。

## 配置科研机器

在科研项目目录中执行：

```bash
# 保存平台提供的 SSH 连接
loaf machine add autodl --ssh "ssh -p 12345 root@example.com"
loaf machine use autodl

# 可选：检查连接、远端目录、工具和磁盘
loaf check
```

已有 SSH Host 别名时，使用 `loaf machine add lab --ssh lab-gpu`。跳板配置仍由 SSH Config 管理，也接受 `-J`、密钥、端口和显式 `-F` 配置文件。粘贴内容只能包含连接参数，不能夹带远端命令或 Shell 表达式。修改已有别名时使用 `--replace`。

用 `loaf machine list` 列出已保存机器，`loaf machine show lab` 查看一项，`loaf machine remove lab` 移除别名。这些命令只读写本地设置，不连接远端。移除别名不删除历史任务或远端数据；仍选择该别名的项目需要显式重新选机，不改写项目 TOML，也不暗中选择其他机器。

运行 `loaf machine -i` 可搜索本地别名、打开详情或注册连接；`machine add/use/show/remove`
带 `-i` 且缺少输入时会引导补齐，完整调用仍直接执行。JSON 和普通直接调用必须提供这些输入，
查询可用 `machine list --json`。别名为 1–64 个英文字母、数字、`_` 或 `-`，首字符须为字母或数字。

在绑定云实例的机器详情中，选择「查看云实例」可只读查询该实例。查询使用当次选中条目的
保存绑定，后续别名或默认账号／产品变化不会让查询改向其他实例。

注册别名不会自动为项目选机。选择器只改变本次操作；「设为本项目机器」或 `machine use`
才保存项目选择。替换共享别名会影响使用它的各项目今后的新操作，旧任务仍保留原绑定。

`machine add` 仅在本地保存连接，`check` 才会实际连接。JSON 和非交互终端命令不会询问密码或主机信任；请先用原生 SSH 建立主机信任。这些静态机器之后的认证需要通过密钥、SSH agent 或原生 SSH 连接复用完成；没有连接复用的单次密码登录不会让后续命令自动获得认证。Loaf 不保存密码，也不关闭主机密钥验证。

需要新租用 AutoDL 实例时，请看[租用并接入 AutoDL 算力](cloud.md)。

### 显式检查远端封装部署

```bash
loaf check --runner
```

普通 `check` 只读取远端环境。`--runner` 会额外将匹配的内置封装上传到私有、带版本的 Loaf 目录，并验证版本；不启动实验，也不安装 Python、uv、CUDA 或项目依赖。

Python、uv 和 GPU 工具属于可选检查项，缺失时不会让能够执行 CPU 程序的机器检查失败。缺少 rsync、不支持的平台或不可写的远端位置则会报告问题。

普通 `check` 还会报告带时间戳的 CPU 数量、总计/可用内存及可读取的逐卡显存和利用率。
缺失值保持未知，不表示为零。这些观察不预留资源，也不证明可以安全抢占外部任务。
Run 的最小前置检查不执行这些额外探针。

### 多目标连接检查与诊断导出

```bash
loaf check --targets lab-a,lab-b --timeout 20s --trace --report /tmp/loaf-diagnostic.json --json
```

`--targets` 一次检查 1–32 个不同别名，不能与 `--target` 同用。每个目标拥有独立超时，机器模式最多并发 4 个，交互认证串行；失败行与未执行行不会掩盖成功结果。逐项报告连接、认证、平台与 runner 检查状态；普通 check 远端只读，部署／修复仍须 `--runner`。

`error.failure_kind` 和 trace 区分认证、主机信任、名称解析、拒绝、超时、重置、关闭及原因未知的传输故障，不推断是网关还是本地网络。首次连接失败后的成功不计作已建立连接的重连，后者单独计数。

`--report` 原子写入 0600 的白名单诊断：目标仅保留输入序号、已知平台标签、各检查状态、稳定错误分类、时间与安全 trace。报告不含别名、端点、路径、命令、请求正文、环境或凭据；普通 check JSON 仍保留自己的可用诊断。报告文件必须不存在或已有已知格式的 Loaf 诊断，拒绝符号链接与未知文件；报告不依赖 `--trace` 才能采集安全 trace。


## 运行实验

在项目目录中提交原本准备在远端运行的命令：

```bash
loaf run -- python3 train.py
loaf status
loaf logs
```

`run` 自动检查执行前提、部署匹配封装、同步工作源码，并在同一项目锁内复制独立工作目录，随后启动脱离连接的监督进程并返回任务 ID。每次运行前无须另行执行 `check`、`sync`、初始化项目或编写启动脚本。后续同步不改变已有任务的代码。

项目已有命令和可用机器时，`loaf run` 直接提交。只有带 `-i` 且缺少这两类输入时才引导补齐
并核对；明确写错的 target 或无效配置仍报错。JSON、非交互及 dry-run 不通过表单补项。
根概览中的「运行项目」则始终提供本地核对，包含实际 argv、setup、源码体积、输出与配置来源，
不展示环境变量值。

接管成功不表示训练成功。关闭本地终端或 SSH 后，仍可凭 ID 查询：

```bash
loaf status <任务ID>
loaf logs <任务ID> -f
```

直接 `status` 查询默认查看当前项目最近 20 个已提交或提交未确认任务，可用 `--limit` 或 `[ps].limit` 调整数量上限；指定 ID 时只查询该任务，`status -i` 则打开下文的本地浏览器。部分查询失败会保留其他成功结果，不会把训练标成失败。`logs` 省略 ID 时选择最近一次已提交或提交未确认任务，不会退回更旧任务；明确的检查、同步失败不替换最近任务。已有任务保留提交时的连接及远端位置，不随项目默认值改变，指定 ID 的查询不能用 `--target` 改向。

任务 ID 保存到本地后若准备失败，错误结果仍保留该 ID 和原绑定。已保存的 `submission: "preparing"` 记录可通过 `inspect <任务ID>` 或 `status <任务ID> --local` 精确查看，但不进入历史列表或最近任务选择。实时 `status <任务ID>` 查询同一绑定，不会启动或续启执行。准备完成不等于确认提交。

需要 runner 的操作必须与本地 CLI 同版本。Loaf 自动部署并验证内置封装，缺失或版本不符时会修复；不下载任意版本，也不热替换正在运行的监督进程。其他版本创建的任务返回 `VERSION_MISMATCH`；升级时请并存保留对应 CLI，并显式使用它操作原有任务。未知记录格式会直接拒绝，不改写数据。

### 提交前完全离线预览

```bash
loaf run --dry-run --json -- python3 train.py
```

预览包含目标及选择来源、字面入口 argv、setup 是否配置、环境变量名（不含值）、实际工作文件及源码字节数、输出选择、等待和同步／下载策略，以及逐字段的 `flag`／`project`／`user`／`default` 来源；本地 `loaf machine use` 为 `local_selection`。未注册项目不分配 ID，也不会读取凭据、连接 SSH、部署 helper 或写入状态。它不冻结快照、不估计网络流量，也不证明远端依赖可用。普通提交仍自动执行先决检查与同步。

### 寻找任务与离线查看

`loaf status -i` 先显示当前项目保存的记录，不主动查询远端。可选择任务，或在「搜索与操作」
中选择「实时刷新」。搜索只检查已加载行，精确筛选先于查询上限生效，返回保留筛选与选中位置。
`~` 表示带日期的旧观察，`Q!` 是本次查询失败，`Q~` 是已保存的查询失败，`S!` 是本地保存失败；
旧观察不表示实时状态。

明确 ID、列表选项、JSON 或管道调用保持直接语义，`status --wait 0s`、`status --local=false`
也不进入菜单。「打开已保存的 ID」可查看不在历史列表中的准备记录。任务动作使用所选准确 ID
及原绑定，不重新解析 latest。

从概览运行项目后，提交结果与后续动作同屏显示。可查看任务、日志或输出，再返回同一结果；
「详情」按需打开，不会重新提交实验。清理后按本地回执撤下日志、输出入口，不重新连接或重跑。
任务详情中的「订阅此任务通知」可选择接收方、
核对历史事件回放后，为这个准确 ID 保存订阅；保存不代表开始派发通知。

提交时为任务指定便于识别的名称和可重复标签：

```bash
loaf run --name "ablation-seed1" --tag ablation --tag seed1 -- python3 train.py
loaf status --name "ablation-seed1" --tag seed1 --json
loaf status --local --tag ablation --json
loaf inspect <任务ID> --json
```

名称和标签在提交时保存于本地，之后不可替换。名称不必唯一，任务操作仍以 ID 为身份。名称／标签按精确值在有界查询前筛选，重复标签要求全部匹配；不改变命令、远端环境、路径或最近任务选择。

`status --local`（含指定 ID）与 `inspect <任务ID>` 只读取本地资料，不启动 SSH、不查询云平台、不访问凭据或部署封装，也可查看当前 SQLite 格式内记录的旧软件版本任务和已删除别名；不导入 v0.5 之前的 JSON 历史。JSON 标记 `source: "local"`；除已有清理收据保留的终态外，执行状态为 `unknown`。本地模式拒绝实时 `--status` 筛选及非零 `--wait`。

`inspect` 包含保存的 `version`、当前 `client_version`、`compatible`、冻结的机器／实例身份、提交确认、位置和输出路径，不暴露 SSH 参数、凭据引用或私有执行环境。保存任务的版本错误还提供 `error.expected_version`、`error.actual_version`。旧可执行文件可按版本命名保留，例如 `loaf-v0.4.0-rc.1`，不要覆盖管理运行中任务所需的唯一 CLI。这些命令不下载 CLI，也不迁移任务。

### 筛选任务历史

```bash
loaf status --status failed --status stopped --limit 50 --json
loaf status --since 2026-10-01T00:00:00Z --target lab --json
```

`--since`、`--until` 接受 RFC3339 时间，筛选保存的提交尝试时间，而非结束时间。列表的 `--target` 按原绑定别名筛选，包括已经移除的别名。先按时间、别名选择，再进行有界远端查询；状态筛选只作用于这些观察，不会无限往前搜索以填满数量上限。JSON 显示候选、已查、匹配、未确认及未检查范围；查询失败的行即使不匹配状态也会保留 `query_error`，不会伪装成没有任务。指定单个 ID 时不能叠加列表筛选。

### 短任务：有界等待结果

```bash
loaf run --wait 10s --json -- python3 smoke.py
loaf status <任务ID> --wait 30s --json
```

从远端确认接管后计算等待预算：窗口内结束时，一次返回终态、退出结果和有限日志；尚未结束时，返回已观察到的状态和原任务 ID，任务继续运行。`--wait 0s` 在接管后返回，最长可等待 5 分钟。这是观察预算，不是训练时限；取消观察不会停止已接管任务。JSON 在确认接管后保留 `submission: "submitted"`，即使之后观察失败、返回 `status: "unknown"` 和错误；`submission: "uncertain"` 则表示仍需查询同一 ID 确认接管。

普通模式显示准备阶段，并在 `run --wait` 期间持续显示日志。JSON 模式只返回一份有限结果，不混入进度文字。持续日志使用 NDJSON：

```bash
loaf logs <任务ID> --tail 100 --json
loaf logs <任务ID> -f --json
```

任务的「日志」页面可选择最近日志或持续跟随。尾部行数留空沿用项目／用户设置，跟随时填写 `0`
可跳过初始尾部、只看新增输出。「游标与恢复选项」提供保存游标、游标文件和断线等待预算。直接 `logs` 命令
仍不经过菜单，省略 ID 时保留原 latest 规则。

日志默认取最后 100 行，日志文本最多 256 KiB，`log.truncated` 表示省略了输出。持续读取产生 `data.type = "chunk"`，结束时为 `"end"`，任务查询结果在 `data.run` 中；流错误为 `"error"`，带普通错误对象。偏移量按原日志字节计算。程序输出不翻译、不解析成指标；及时看到日志还需要程序主动刷新输出。

若实时日志需要承受一次临时断线，可显式启用续接：

```bash
loaf logs <任务ID> -f --reconnect
loaf logs <任务ID> -f --resume "$cursor" --json
```

有限日志查询通过 `data.log.cursor` 返回游标，流式 chunk 使用 `data.run.log.cursor`。应在消费完该块后保存游标。`--resume` 须配合 `--follow`，不能与显式 `--tail` 同用，并从已确认的原始日志字节偏移续读；与 `--reconnect` 一样，最多允许一次临时断线重连。默认 `logs -f` 仍保持单连接行为。

游标绑定软件版本、任务、位置与日志文件，并校验偏移边界内容。文件被替换、截断或边界内容被改写时返回 `LOG_CHANGED`，不会悄悄从尾部重新开始；此时须显式读取新尾部以获取新游标。输出消费失败、取消、认证／信任失败或已交付 end 事件都不触发重启。已接管任务独立继续运行。

显式重新取尾部会重置连续性，不能补齐整个断线缺口。日志流未收到 `end` 就关闭，不代表
训练已经结束，应查询同一任务状态。Ctrl+C 只结束观察，不停止执行。

提交回执丢失时，Loaf 查询同一个已保存 ID 一次。仍无法确认就返回 `SUBMISSION_UNKNOWN`、原 ID 和操作退出码 3。此时应查询原任务，不要自动另开一次运行。

### 恢复日志会话与多任务等待

```bash
loaf logs <任务ID> -f --resume-file /tmp/loaf-cursor.json --recovery-budget 2m
loaf wait r_A r_B --timeout 30m --json
loaf wait --tag ablation --timeout 1h
```

`--resume-file` 读取已确认处理的游标并启用恢复。人类终端输出仅在写入成功后保存游标。JSON 和管道输出**不会自动确认下游已处理**：先处理一块数据，再用 `loaf logs <任务ID> --resume-file /tmp/loaf-cursor.json --ack-cursor "$cursor" --json` 保存其 `data.run.log.cursor`。确认操作完全离线。游标文件应放在源码树之外；原子写入不能回退，未知格式和符号链接会被拒绝。

未显式指定 `--recovery-budget` 时，启用恢复仍只重试一次。正预算允许在累计断线等待时间内连续恢复（最多 24h），正常日志流不消耗预算；恢复提示写入 stderr。默认 `logs -f` 不变。输出接收失败、取消、认证或信任失败、结束事件及 `LOG_CHANGED` 均不会重启流。

成功观察在 SQLite 中保存公开的 `last_observation`，包含观察时间、阶段和退出结果；`last_query_failure` 只记录最近失败查询的时间、错误码和阶段。离线 `status --local` 和 `inspect` 展示这些带时间的事实，不将其作为实时状态；不缓存日志正文或执行环境。

本地状态或游标 checkpoint 写入失败单独通过 `state_error` 返回稳定错误码，历史列表和 `wait` 用 `state_errors` 计数，不计为查询失败。本地保存或输出消费失败可以使本次操作失败，但不抹掉已验证的远端状态或退出结果，也不写成 `last_query_failure`。消费失败的日志块不会被确认处理。

checkpoint 保存失败时，修复游标文件后从其已保存位置恢复；刚显示的块尚未确认，可能再次
出现。另起一次 `--resume "$cursor"` 不会更新原文件；需要继续保存 checkpoint 时，应改用
`--resume-file`，两者不能组合。

`wait` 在开始时冻结明确 ID 或精确标签匹配的本地集合（1–1000 个任务），轮询各自保存的连接，并返回全部任务、已完成／待完成／查询失败及已观察／未观察覆盖数。不可达任务不会妨碍其他任务的观察。人类输出只报告变化，JSON 为单个有限结果。总预算打断后续查询时，保留该行此前完成的观察及 `observed_at`，并用 `query_interrupted` 和 `interrupted` 数量标明，不将本地取消误报为目标故障。超时或取消不会停止任务；这不是作业调度器。

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

可先运行[无依赖 CPU 示例](../../examples/cpu/)或[显式准备示例](../../examples/setup/)。Agent 可使用简短的 [Loaf Skill](../../skills/loaf/SKILL.md)，不需要读取内部文件、PID 或锁。

### 训练并使用一个小型 GPU 模型

[PyTorch 示例](../../examples/torch/)使用生成的数据训练分类器，无须下载数据，但需要已有的 CUDA/PyTorch 环境。进入示例目录，选择机器后按前述方式执行 `loaf run`、`loaf logs` 和 `loaf pull`。若非交互 SSH 的 PATH 中没有 `python3`，请在 `run.command` 或 `run --` 后显式指定已有解释器；Loaf 不读取猜测的 Shell 启动文件，也不自动激活环境。

输出包含 PyTorch checkpoint、指标、模型参数和留出推理样本。训练脚本会重新加载 checkpoint；取回结果后，将下载的任务目录作为参数运行 `python3 verify.py`，即可在本地独立使用模型参数推理。验证只需要 Python 标准库，不需要本地 PyTorch。GPU 示例由用户显式运行，不进入仅用 CPU 的 CI fixture。


## 检查机器与转发服务

在「机器与连接」中打开已保存机器，可检查连接、进入 SSH、执行前台命令或建立隧道。
这些动作沿用本次所选别名，不改变项目默认机器。完整命令则直接进入原生流：

```bash
loaf exec -- nvidia-smi
loaf exec -- printf '%s\n' 'a b' '$HOME' ''
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

除 SSH 连接时间外，确认最多等待宽限期加 5 秒，上限为 5 分钟。超时、确认丢失或客户端取消时，返回 `STOP_UNCONFIRMED` 和操作退出码 3；远端已接收的请求继续执行。请用 `loaf status <任务ID> --json` 查询同一任务。监督进程身份失效时如实报告，不按可能复用的 PID 盲目发信号。已知接收策略时，JSON 包含 `data.stop_grace_millis`。


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

### 浏览输出与批量对比

```bash
loaf outputs <任务ID> --depth 2 --limit 200 --json
loaf outputs <任务ID> --prepare-runner --json  # 明确允许部署或修复辅助程序
loaf pull r_A r_B --into ./comparison --json
loaf pull r_A r_B --into ./comparison --concurrency 2 --json
```

`outputs` 仅浏览任务冻结的输出范围，返回相对路径、类型、大小、修改时间及缺失根目录，并报告已检查根目录与条目的覆盖情况。深度相对于各输出根目录（根为 0），范围 0–20，条目上限 1–1000。`truncated` 和 `depth_limited` 明确标出未访问部分。不读取内容、不计算校验和、不遍历符号链接，也不要求任务成功或仍在运行。默认不会上传辅助程序，缺失或不匹配时返回 `RUNNER_REQUIRED`；只有 `--prepare-runner` 明确允许修复。`pull` 的自动准备行为不变。

从交互概览进入「结果与清理」，先选择明确任务，再浏览元数据并核对下载。
选择目录会包含后代文件；浏览深度不限制后续目录下载。直接调用 `outputs` 或 `pull`
仍保持原行为，不增加核对表单。

用引号保留文件名中的空格和通配符字符：

```bash
loaf pull r_A --path 'outputs/模型 [1]*?,.txt' --to './review copy' --checksum=false --json
```

文件将写入 `./review copy/outputs/模型 [1]*?,.txt`，不会追加 `r_A`。

多个明确 ID 必须使用 `--into`，各自根据保存的连接和输出路径下载到 `<into>/<任务ID>/`。JSON 保留每个结果或错误及完成、失败、未执行数量，单个失败不会丢弃成功结果。默认串行，`--concurrency 1–4` 显式启用并发，交互认证保持串行。重复下载是增量更新，不删除本地额外文件。单任务 `--to` 仍表示精确目录，不能与批量 `--into` 混用。一次性下载目录若位于源码树中，需要普通忽略或排除规则。

若批量下载只有 `r_B` 失败，重试时保留它原来的子目录：

```bash
loaf pull r_B --into ./comparison --json
```

不要改用 `--to ./comparison`，否则目的地会改变。缺少 `changed_files` 表示计数不可用，
不能当成零。

人类输出还会合计实际访问到的普通输出文件。此有界统计不包括未访问的输出内容、
工作输入和日志，不代表整个 Run 占用。规划下载前请同时检查覆盖范围。

## 回收已结束任务的数据

```bash
loaf clean <run-id> --dry-run --json
loaf clean <run-id>
loaf clean <run-id> --yes --json
```

清理必须提供明确 ID，不隐式选择最新或全部任务。预览显示原绑定机器及将删除的数据，不部署新封装或删除文件。实际清理时会重新检查归属、终态和锁，预览不等于已经保留了删除许可。普通模式询问一次确认，JSON 或非交互删除需要 `--yes`。

从任务详情或「结果与清理」选择准确任务后，先核对路径和资格。混合不合格项的批次不会悄悄
只删除合格行；确认默认保留远端数据。恢复操作只把未完成的原 ID 带回新预览，并再次确认。

清理会删除所选任务的远端工作文件、日志和输出；需要保留的内容请先取回。本地已下载结果不受影响，但删除的远端数据无法撤销。只有已确认提交、且本次复查确认终态的任务可以清理。运行中、提交未确认、归属不明或结果未确认的任务会被拒绝，不隐式先停止。`clean` 目前不支持回收未提交的准备数据（`submission: "preparing"`）。其他任务、源码缓存、共享数据和环境不在清理范围。

保留少量删除回执与 ID 声明，防止延迟到达的请求再次执行相同任务。`status` 保留原执行结果并标记清理信息；`logs`、`pull` 和 `stop` 返回 `RUN_CLEANED`。清理最新任务不会让默认命令悄悄退回旧任务。部分失败或确认丢失时，查看报告的 ID，仅对这些明确的原 ID 重试。


## 同步工作代码

概览中的「更多 → 核对源码同步」先显示本地选择，仅在明确选择「开始同步」后连接。
改选机器会回到核对页，不保存项目默认值。

在项目目录中，先预览选中的文件，再上传：

```bash
loaf sync --dry-run
loaf sync
```

预览完全在本地进行，不需要配置目标、连接 SSH 或部署封装，也不会创建项目状态。实际同步会检查目标、部署匹配的内置封装，然后调用系统 rsync 通过 OpenSSH 传输；不会安装实验依赖或启动训练。

源码写入 `<remote_dir>/cache/`。首次同步要求项目目录不存在或为空，后续同步检查项目标记。不要选择已经放有数据或环境的科研目录；这些内容应放在 `cache/` 旁边，而非里面。

默认的 **镜像模式（mirror）** 只在 `cache/` 内清理已删除或新被排除的源码，不影响项目其他目录或已有运行副本。项目锁防止源码更新与运行副本准备互相干扰。传输失败或取消后，缓存不能用于新运行，直到显式重新同步成功；已有运行不会因此停止。缓存采用增量更新，不是整个目录的原子替换。

预览中所选源码达到 1 GiB 时，人类模式会提示检查体积。传输前请检查排除规则，
源码字节数不等于增量网络传输量。

### 选择上传内容

- Git 项目通过原生 Git 选择已跟踪的工作文件及未被忽略的新文件，包括未提交修改，保留 Git 跟踪以及嵌套忽略、否定规则的语义。非 Git 项目读取嵌套 `.gitignore`。
- Loaf 还会排除虚拟环境、常见缓存、`.git`、`.env`/`.env.*`、常见私钥名称、`*.pem`、`*.key`、`*.pt`、`*.pth`、`*.ckpt`、`*.safetensors` 和 `loaf-results/`，即使它们已被 Git 跟踪。配置的输出路径及当前本地结果根目录也被排除，默认输出是 `outputs/`。
- 保留指向已选项目文件或目录的安全相对符号链接，不遍历目录链接。绝对、越界、失效及目标未选中的链接会跳过并报告。嵌套仓库与子模块不展开，请单独同步。
- `sync.exclude` 追加 Gitignore 风格的正向排除规则，不接受用 `!` 恢复默认排除项。预览显示排除结果和原因。默认规则按名称匹配，不是凭据扫描器。

`selected_bytes` 是选中普通文件的大小之和，不是网络传输量；`changed_files` 统计实际传输文件内容及变更的链接目标，不计删除和仅时间戳、权限变化。预览选择当前工作路径，不会在继续编辑时冻结本地文件。

## 准确构造参数

```bash
loaf run -- python3 train.py --label 'seed 1' --rate -1 --json
loaf pull ID --path 'outputs/a,b.csv' --path 'outputs/seed 1.pt'
loaf status --tag ablation --tag seed1
loaf sync --dry-run --respect-gitignore=false
loaf run --wait 0s -- python3 train.py
```

`run --` 后的 `--json` 属于实验程序；引号包裹的空字符串仍是一个空 argv 元素。
`--path`、`--tag`、`--status`、`--exclude` 接受字面值，重复选项表示多个值；
`--path` 不展开 glob。`check --targets`、云资源 `--gpu-model` 和 `--region`
支持 CSV 与重复选项。操作允许时，单独 `--region=` 可用空列表替换配置中的地区。

布尔覆盖使用 `--checksum=false` 或 `--respect-gitignore=false`。
省略值会继承低优先级配置；false、零、空值不等同于省略。
`--wait 0s` 显式不等待，`status --limit 0` 则是错误。
时长需带单位（`10s`、`2m`），内存下限带 `GiB`，计算价格上限使用十进制字符串。

需要 Shell 语法时，显式调用 Shell：

```bash
loaf exec -- /bin/sh -c 'printf "%s\n" "$HOME"'
```

交互命令编辑器提供项目命令、字面 argv 和显式 Shell 三种模式。字面模式只用引号和反斜杠
组合参数，不执行变量／命令替换、glob、管道或重定向。新输入上限为 4096 字节，超长时拒绝，
不会截断后执行；已有 argv 可原样保留。多行脚本应放入文件或项目命令。
前台 exec 需要自己的明确命令，不继承项目的实验命令。

结果提供 `next_actions[].command` 时，直接把数组作为 argv 传递，不按空格拆分显示文本。
Loaf 的 `--json` 应放在 `--` 之前。CSV 项内含逗号时，还需使用 CSV 引号，
例如 `--region='"region,one",region-two'`：外层引号交给 Shell，内层引号属于 CSV。
重复的字面 `--path` 不需要 CSV 编码。

再次运行时，明确提供所需命令，或明确使用当前项目命令。公共任务详情不是可直接执行的
历史配方：它不导出私有环境变量值或 setup 内容，当前配置也可能与旧任务的冻结输入不同。
