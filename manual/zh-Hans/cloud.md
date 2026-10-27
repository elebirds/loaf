# 显式使用 AutoDL 资源

[首页](../../README.zh-Hans.md) · [命令参考](commands.md) · [English](../en/cloud.md)

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
loaf machine use gpu-a
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

操作收据保存在本地 SQLite 的私有记录中，先提交意图，再发送付费或电源请求。创建中的网络中断、取消、无效响应或结果不明的服务器错误返回 `CLOUD_UNCONFIRMED`（退出码 3），保留请求 ID 及已知实例 ID；不重放该请求、不自动租用替代实例。未确认的分配意图会阻止此账号继续租用；只有明确映射的无库存拒绝，才会在原授权时间范围内进行下一次串行尝试。

创建结果不明时，先核对平台控制台，再关联已知实例 ID。如果人类已确认**没有创建实例**，可用 `cloud reconcile <请求ID> --no-instance --yes` 解除保护，不能从一次有界列表自动推断未创建。开关机操作凭据通过查询原实例核对，reconcile 不重复发送开关机请求。

`cloud start/stop` 操作**整个实例**，而 `loaf stop` 只停止一个实验。平台接受开关机请求不代表完成，通过 `--wait` 或 `info` 观察；关机可能影响其他用户或任务，存储仍可能计费。Loaf 不释放实例、不在训练后自动关机，也不实施运行时长或总费用限制；租用不自动启动训练，`run` 不隐式租卡。
