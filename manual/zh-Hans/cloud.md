# 显式使用 AutoDL 资源

[首页](../../README.zh-Hans.md) · [命令参考](commands.md) · [English](../en/cloud.md)

## 选择操作

使用直接命令管理资源：

```bash
loaf cloud login --prompt autodl-main
loaf cloud list --account autodl-main
loaf cloud info INSTANCE_ID --account autodl-main
loaf cloud acquire -i
```

GPU 获取提供专用交互流程，用于比较候选和核对费用。其他操作使用命令参数，
需要时只做一次确认。裸 `loaf cloud` 无论是否带 `-i` 都显示离线帮助。
自动化使用 `--json`，不可与 `-i` 合用。

## 用一个向导租到 GPU

```bash
loaf cloud acquire -i
```

指定 `-i` 后，Huh 向导分四组收集选择：

1. **算力：** 搜索平台实际 GPU 型号，选择卡数、允许地区；资源下限放在可选的高级条件里。
2. **选卡或抢卡：** 选中当前有库存的主机，或设置整实例小时价格上限，等待符合条件的库存。
3. **环境与存储：** 按框架、Python、CUDA 名称选择基础／个人镜像，再设置磁盘扩容；扩容存储另设每日价格上限。
4. **核对：** 检查账号、产品、镜像、费用和条件，填写机器别名；输入 `yes` 授权最多创建一台实例。

地区按平台数据中心分组，同一展示地区只列一项；选择“西北B区”会包含该组全部内部区号。已有参数或模板若限定单个区号，向导保留并标明这个精确范围，只有显式选择“全部”才扩大到整组。`--region` 和目录 JSON 仍使用规范区号。

未设置地区偏好时，向导预选平台标记的公开地区；企业区仍显示，需要时直接勾选。已有地区设置优先，包括显式空列表。全部取消勾选表示不限地区，企业区也在范围内。非交互命令沿用原语义：未配置地区条件即不限地区，不套用向导的预选值。

页头持续展示完整流程：**方式 → 算力 → 主机/条件 → 环境/磁盘 → 授权 → 获取 → 接入**。`●` 标出当前步骤，`✓` 表示之前已完成的选择，`○` 表示后续步骤；当前步骤展开填写，返回时位置随之更新。

方向键移动，`/` 搜索，空格多选，Enter 继续，Shift+Tab 在字段间返回。涉及选择的页面提供返回／取消；Ctrl+C 取消。普通终端使用编号菜单：Enter 保留带标记的选择，逗号分隔编号多选，`0` 清空。已有参数和模板成为预填值；普通完整命令直接执行；带 `-i` 时进入核对，`--yes` 跳过向导。不加 `-i` 时缺项直接报错。只有一个已保存账号时可为本次调用预选，不更改全局默认；未配置账号时可从原生登录返回获取流程。

完整模板直接进入核对；需要修改时再返回对应页，保留草稿。核对页可查看不含付费批准的等价命令。保存模板使用 `cloud profile save`。

**选卡固定一个候选。** 核对时按所选环境和存储重新报价；库存或价格变化后回到选择，不暗中买另一台。网页 Pro 候选是型号／地区／规格组合，不是物理主机预留。首轮向导支持按量计费、基础／个人镜像，不提供包周期计费、优惠券和社区应用创建。

候选按价格、GPU／显存、CPU、内存、地区和 ID 对照，默认价格升序；可切换 CPU／内存降序，未知值排后。支持刷新和加载下一批。普通实例的每轮巡检受 `--limit` 限制，后续轮询继续后页、到末页后重新开始；不同时间的窗口不合并宣称全量快照。后页固定候选仍复查同一窗口的同一 ID；复制命令中的 `loaf-offer-v1:…` 是定位提示，不是预留。网页 Pro 保持其聚合库存语义。

**抢卡先冻结条件，再等待。** 授权后持续轮询，匹配时自动申请，不再临时弹出购买确认。前台进度显示当前阶段、已等待时间、下次实际轮询、轮询／申请次数和库存覆盖；默认显示主要阻碍和当前检查范围；Tab 查看完整统计。分配前按 `e` 停止旧等待并返回修改，再次核对授权；一旦已创建或结果不明，只恢复原资源。启动、磁盘复制、SSH 接入分别展示。库存何时到来未知，不显示虚构百分比；界面的计时刷新不增加云请求。

向导和进度原位更新，只展示当前阶段；进入 SSH 接入后不再保留库存统计。型号、预算、环境和费用说明在授权页统一核对，技术 ID 与拒绝原因放在进度详情中。结束后只留一份结果和下一步命令；SSH 尚未确认时，结果明确保留原实例，并给出继续接入的命令。

`--continuous` 显式持续等待直到成功或取消；`--wait 30m` 有界等待，`--wait 0s` 仍只尝试一次。`--continuous` 不与显式 `--wait` 同用，`--offer` 不与监测模式同用。命令须保持运行；取消不关闭已创建实例。新的持续／固定候选请求必须有计算价上限，收费扩盘还必须有 `--max-storage-daily-price`。这些不是总费用限制。

### 发现选项并复用偏好

```bash
loaf cloud catalog gpu --query 4090
loaf cloud catalog region
loaf cloud catalog image --query PyTorch
loaf cloud catalog image --kind private
loaf cloud account use autodl-main
loaf cloud profile list
loaf cloud profile show train
loaf cloud profile save train --gpu-model 'RTX 4090' --image IMAGE --max-compute-hourly-price 3
loaf cloud profile remove train --yes
```

目录提供规范值，不代表实时库存。`--query` 搜索名称／ID，`--limit` 限制返回项并保留覆盖信息。普通地区和 Pro 数据中心不是同一种值。Pro 地区目录明确标记为已知控制台子集、覆盖不完整；开发者令牌 Pro 不提供目录。向导搜索真实有效的镜像版本组合，不拼出不存在的环境。新的固定／持续请求检查镜像元数据兼容性，不验证安装包及实验依赖。

`profile save` 按现有合并规则离线保存生效偏好，租用前也能保存；同名个人模板需 `--replace`。`remove` 只删除个人模板，交互确认或非交互 `--yes`；同名项目模板继续生效。模板展示来源及字段覆盖关系，不修改项目文件。

### 非交互调用使用同一流程

```bash
# 离线本地校验：不读凭据、不联网、不分配 ID、不写状态、不预留资源。
loaf cloud acquire gpu-a --profile train --dry-run --json

# IMAGE 是对应目录返回的规范镜像 ID。
loaf cloud acquire gpu-a --account autodl-main \
  --gpu-model 'RTX 4090' --image IMAGE \
  --max-compute-hourly-price 2.50 --continuous --yes --json

# 固定 search 返回的候选，不换成另一台更便宜的机器。
loaf cloud acquire gpu-a --account autodl-main --offer OFFER_ID \
  --image IMAGE --max-compute-hourly-price 2.50 --yes --json
```

Dry-run 返回生效值、逐字段来源、待执行的实时检查，以及 `next_actions` 中可回读的规范 argv（不含凭据或 `--yes`）；显式零、空列表和 false 保留。克隆预览可本地解析已保存的源别名；源镜像／地区／磁盘继承保留为待实时校验。预览成功不证明库存、报价、克隆就绪或 SSH 可达，也不成为获取的必经前置步骤。

有限 JSON 的 stdout 仍只有一个结果。默认 `--json` 不输出进度；显式 `--progress=json` 才在 **stderr** 输出版本化 NDJSON。`--progress=plain` 输出低频阶段更新，`none` 关闭运行进度。云结果增加 `context` 和 argv 形式的 `next_actions`，保留原有按阶段变化的 `data`。失败后沿用已知请求／实例：

```bash
loaf cloud bind --request REQUEST_ID --json
```

该命令从收据恢复别名及冻结资源，不重新租用。`cloud info`、`cloud start`、`cloud stop`、`cloud bind` 使用准确 ID、已绑定别名或保存的请求，不打开实例管理菜单。整机电源操作仍须单独确认。

## 租用并接入 AutoDL 算力

已有静态 SSH 机器不需要配置云账号。AutoDL 可用主账号手机号或完整的 `子用户名@主账号ID` 登录；同一已验证会话按账号权限访问**普通实例**（`standard`）和 **Pro**（`pro`）。密码输入不回显，不提供接收密码值的命令行参数。

```bash
loaf cloud login --prompt autodl-main --username 'your-subuser@master-id' --default
loaf cloud list --account autodl-main
loaf cloud list --account autodl-main --product pro
```

需要原生用户名／密码输入时，显式使用 `--prompt` 或 `-i`；`--verify` 显式请求验证码输入。不指定这些选项时，请提供 `--username`、`--password-stdin` 等输入，或复用已有凭据。认证仍在一个原生输入会话中完成。

### 选择凭据保存方式

```bash
loaf cloud login --prompt autodl-main --credential-store keyring   # 默认
loaf cloud login --prompt autodl-main --credential-store file      # 未加密的私有文件
```

`keyring` 使用 macOS 钥匙串或 Linux Secret Service（需要可用的用户会话）。`file` 不依赖凭据库服务：保存在 `$XDG_STATE_HOME/loaf/credentials/`，默认 `~/.local/state/loaf/credentials/`，目录权限 `0700`、文件权限 `0600`。文件模式能隔离其他系统用户，不能防止以你身份运行的软件读取。Keyring 失败时不暗中改写文件。原生 Windows 的运行条件见[安装说明](installation.md)；Windows 原生 keyring 验收尚未完成。

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
loaf machine use gpu-a
loaf run -- python3 train.py
```

`credentials` 显示当前 SSH 地址，默认隐藏密码；只有显式 `--show-password` 才会显示密码。Provider 连接默认使用 OpenSSH `accept-new`：在原命令中自动接受并记录首次主机密钥，包括 JSON 调用，无须另开 SSH 会话或确认提示。已知密钥变化仍会拒绝，显式目标主机密钥设置优先。云连接优先使用密钥，也能通过 OpenSSH 原生 askpass 使用临时获取的密码。密码只在内存中，经私有的临时本地套接字传递，不写入配置、子进程参数或环境变量；临时启动脚本不含密码。

云 runner 操作为每个绑定实例及连接配置使用本次命令私有的 OpenSSH 主连接；连接健康期间只解析一次连接信息，探测、封装请求和原生 rsync 共用该认证连接。Loaf 不借用已有主连接，也不改写 SSH 配置。正常退出和取消只关闭本命令创建的连接；父进程生命周期管道保证 CLI 被强制结束后也不会留下持久连接。上述私有主连接策略不改变静态目标及前台 `exec`、`ssh`、`forward` 的连接归属规则。

`bind` 先检查账号能读取该实例，再保存绑定并验证 SSH 认证。`ready` 仅表示平台运行、SSH 认证成功且机器已保存，不表示 Python/CUDA 环境已就绪。`acquire/bind --ready-timeout` 默认 5m，最大 10m，0s 表示仅做一次有界检查。实例已创建但连不上时可能已经计费，请保留 ID 并修复连接，不要重新租卡。可用 `loaf cloud bind --request <请求ID>` 恢复同一租用结果，不会创建替代实例。显式 `--replace` 可重绑别名，但历史任务仍刷新其原实例的连接信息。


接入每次仅执行固定 `/bin/true` 认证探测，最多 10 秒，暂态连接失败或暂态平台信息读取失败间隔 5 秒重试。主机信任、认证和配置错误立即返回明确分类及动作；探测次数与付费申请次数分开。普通前台 `exec` 不会自动重放。

修复连接问题后，对同一实例或保存的请求重试 `cloud bind`，首次主机信任无须额外步骤。
主机密钥变化时须人工核实，Loaf 不自动移除旧密钥。对同一冻结绑定重试幂等，
不同实例仍需显式 `--replace`。


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
loaf machine use gpu-a
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

其他条件包括 `--gpu-vendor`、`--gpu-model`、`--min-cpus`、`--min-disk` 和 `--region`。型号和地区使用平台的准确名称/ID，通过 `cloud catalog` 或向导选择，不要猜测。内存、显存、磁盘下限必须带 `GiB` 单位。普通实例数据盘扩容使用 `--expand-data-disk`，Pro 系统盘扩容使用 `--expand-system-disk`，两者按平台的 GB 约定传入。租用前不验证镜像是否能拉取或其中依赖是否就绪。镜像按产品区分：`--image` 指定普通实例基础镜像 UUID 或 Pro 镜像 UUID；两种网页会话产品的私有镜像改用 `--private-image`，二者不能同时设置；开发者 Pro 使用其独立的镜像 UUID。

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

数据盘默认继承源实例的扩容量；项目／用户配置及 `--expand-data-disk` 可以增大，不能缩小。计算条件与安全上限仍按原有优先级生效。没有等待偏好时，克隆默认抢占 30m；项目／用户配置或 `--wait` 可以覆盖，包括用显式 `0s` 只尝试一次。有限窗口最长 24h；显式 `--continuous` 则等待直到成功或取消，期间需要保持命令运行。

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

默认轮询周期 10s，可配置 10s–10m 的最小间隔，创建前另做一次即时复查；临时故障增加退避、抖动，并遵守 `Retry-After`。有限等待最长 24h；获取可显式选择 `--continuous`。关闭客户端结束监测，但不撤销已创建的实例。人类输出显示匹配数、拒绝原因、查询覆盖及观察时间；有限命令和 acquire 的 JSON 只返回一个结果，`watch --json` 使用 NDJSON 变化事件、低频心跳和结束/错误结果。运行期间修改配置不会扩大已授权范围。

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

这不是只读库存监测。`--allow-unquoted` 不能绕过有效的价格上限，此时直接返回 `PRICE_UNVERIFIABLE`，不付费；无法核验的通用硬件下限也会拒绝，不暗中放宽。Pro 地区为数据中心 ID，与普通实例的地区标识不同。`118` 是平台的 CUDA 11.8 最低版本代码；Loaf 不通过平台启动命令执行实验。接口依据见 [Pro 官方 API](https://www.autodl.com/docs/instance_pro_api/)、[当前普通实例租用表单](https://www.autodl.com/create) 及 [GET 查询参数实测记录](https://github.com/RordChang/autodl-pro-codex-skill/blob/main/references/api.md)。开发者 Pro snapshot/status 使用查询参数，不自动改为 GET body，也不重放变更请求。

### 核对不确定结果，明确控制计费

```bash
loaf cloud requests --json
loaf cloud requests <请求ID> --json
loaf cloud reconcile <请求ID> --instance <已知实例ID> --json
loaf cloud bind gpu-a --request <请求ID>
loaf cloud start gpu-a --wait 2m --yes
loaf cloud stop gpu-a --wait 2m --yes
```

操作收据保存在本地 SQLite 的私有记录中，先提交意图，再发送付费或电源请求。创建中的网络中断、取消、无效响应或结果不明的服务器错误返回 `CLOUD_UNCONFIRMED`（退出码 3），保留请求 ID 及已知实例 ID；不重放该请求、不自动租用替代实例。未确认的分配意图会阻止此账号继续租用；只有明确映射的无库存拒绝，才会在原授权时间范围内进行下一次串行尝试。

创建结果不明时，先核对平台控制台，再关联已知实例 ID。如果人类已确认**没有创建实例**，可用 `cloud reconcile <请求ID> --no-instance --yes` 解除保护，不能从一次有界列表自动推断未创建。开关机操作凭据通过查询原实例核对，reconcile 不重复发送开关机请求。

`cloud start/stop` 操作**整个实例**，而 `loaf stop` 只停止一个实验。平台接受开关机请求不代表完成，通过 `--wait` 或 `info` 观察；关机可能影响其他用户或任务，存储仍可能计费。Loaf 不释放实例、不在训练后自动关机，也不实施运行时长或总费用限制；租用不自动启动训练，`run` 不隐式租卡。
