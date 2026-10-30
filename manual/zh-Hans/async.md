# 接收结果通知，共享一台机器

[首页](../../README.zh-Hans.md) · [使用指南](guide.md) · [命令](commands.md) · [English](../en/async.md)

任务结束后需要交付结果，使用通知；协作项目需要共享机器，使用队列。
直接 Run 和云资源操作不需要经过队列，也能发送通知。

原生 Windows 支持事件持久化、前台 Bridge、Webhook 和远端队列。基于 Unix socket 的 `codex`、`codex-desktop`、`claude-channel` 适配器在该平台返回 `DESTINATION_UNSUPPORTED`；下面的相关示例适用于已验证的 Unix／WSL 环境，WSL 不代表能够访问 Windows 桌面会话。

## 将通知送回当前 Codex 桌面会话

在希望接收通知的 Codex 桌面会话中，让 Agent 执行一次：

```bash
loaf notify bind here --current
```

Loaf 从该工具 Shell 的 `CODEX_THREAD_ID` 取得精确身份，通过本机桌面的私有 IPC
查询该线程的现有 owner，验证成功后才保存目的地。不需要手填 socket 或线程 ID，
也不会启动另一个 App Server、新建或恢复替代会话。普通终端没有当前会话身份时会明确失败。

以后只引用这个名称：

```bash
loaf run --notify here -- python3 train.py
loaf cloud acquire gpu-a --profile train --notify here
```

在本机另一个终端保持 `loaf notify serve` 运行，或之后执行 `--once` 补取。
绑定命令只做只读验证；真正交付事件可能使目标 Agent 继续处理，并消耗其服务额度。
同一会话重复绑定同名目的地不会重写它；名称已属于其他会话时须换名或明确加 `--replace`，
已有订阅仍保留原冻结目的地。

该模式保存为 `codex-desktop`，使用桌面的实验性 peer IPC，而不是公开 App Server
WebSocket。要求本机目标会话仍由桌面持有，且支持不可信应用输入；协议不兼容时失败，
不会猜最近会话。它保留目标线程自己的工作区、sandbox 和审批设置，不提供 `--cwd` 覆盖。

## 将结果交给明确的目的地

先登记目的地，再在提交时引用名称。请把示例 URL 换成自己的接收端：

```bash
loaf notify destination add results --kind webhook --endpoint https://receiver.example/loaf
loaf run --notify results -- python3 train.py
loaf notify serve
```

`notify serve` 是前台 Bridge，负责接收持久事件并投递。Ctrl-C 仅停止此 Bridge，
不停止 Run、队列或云实例。持续运行可及时交付；也可以之后使用
`loaf notify serve --once` 处理一轮。本地断线后重新连接，仍可补取远端保留的事件。

重复 `--notify` 可以指定多个目的地。`cloud acquire` 的同名选项订阅资源获取、
连接就绪及需要处理的情况。库存匹配不是已经获取实例，实例已创建也不一定可以 SSH 连接。
通知不会扩大为再次购买资源或提交新任务的授权。

手工接入公开 Codex App Server 时，可登记明确会话：

```bash
loaf notify destination add assistant --kind codex --session THREAD_ID --endpoint ENDPOINT
```

本版实际实现四种适配器：`codex`、`codex-desktop`、`claude-channel` 和 `webhook`。
`claude-resume` 是保留模式，当前不支持：Loaf 会以 `DESTINATION_INVALID`
拒绝登记，SDK 返回 `ErrUnsupported`。本版没有 headless/resume 适配器，
也不会自动回退到此模式。

使用所选适配器支持的接入端。保存绑定不等于证明可连接或可处理。
Loaf 不猜测最近会话、不静默新建替代会话，也不绕过原工作区和审批要求。
Claude Channel 要求会话开启且平台允许。

`notify destination list` 只显示安全信息，不显示私有连接详情。
独立 Go 投递 SDK 负责明确目的地及投递证据，不负责 Run 调度或训练 Runtime。

## 使用发行版二进制绑定开启的 Claude 会话

可选 Channel 适配器已包含在 `loaf` 中，不要求安装 Bun、Node 或另一个 helper。
每个开启的 Claude 会话应使用独立 socket 和唯一标签。先创建权限为 `0700` 的
socket 父目录；helper 新建 `0600` socket，拒绝占用已有 socket 路径。

在该 Claude 项目的 `.mcp.json` 中配置，并替换绝对路径：

```json
{
  "mcpServers": {
    "loaf-events": {
      "command": "/absolute/path/to/loaf",
      "args": ["notify", "channel", "--socket", "/absolute/private/loaf-channels/research.sock", "--session", "research_1"]
    }
  }
}
```

由 Claude 启动此子进程。标准输入/输出仅承载原生 MCP，不是 Loaf JSON 或 NDJSON；
不接受 `--json`、`--trace`，诊断写入 stderr。标签是该 helper 的会话绑定，
不是推测出的 Claude 内部会话 ID。

### 预览期的本地开发验证

当前自定义 Channel 需要 Claude 的交互式开发选择。仅在已审查此本地 helper，
且明确选择测试后运行：

```bash
claude --dangerously-load-development-channels server:loaf-events
```

这是可选开发测试，不是普通启动方式，Loaf 不会自动添加此选项；它也不能覆盖组织
`channelsEnabled` 或其他真实拒绝。普通使用仍以上游准入为前提，详见官方
[Channel 预览要求](https://code.claude.com/docs/en/channels-reference#test-during-the-research-preview)。

会话开启且平台允许后，用同一 socket 和标签登记目的地，再单独运行投递 Bridge：

```bash
loaf notify destination add claude-research --kind claude-channel --endpoint /absolute/private/loaf-channels/research.sock --session research_1
loaf run --notify claude-research -- python3 train.py
loaf notify serve
```

通知写入仅为 `written`。Claude 读到消息后，通过 helper 的 `ack_event` 工具传入
该消息的 `{"message_id":"MESSAGE_ID"}`，才能提供 `processed` 证据。
它表示会话确认已读，不表示后续任务已完成。没有 ACK 则保留未确认，不自动开启第二个 turn。
适配器不提供审批中继；后续操作仍须遵守现有权限。

## 分别查看事实与投递证据

```bash
loaf events list --source project --limit 100 --json
loaf events list --source resources --json
loaf notify deliveries --json
loaf notify subscribe run:RUN_ID --destination results
loaf notify subscriptions
```

来源包括 `local`（已接收 inbox）、`resources`（本地云操作事实）、`project`
（所选远端项目）、`queue`（所选机器队列）。本地和资源来源不接受 `--target`。
`--limit` 为 1–1000；`--cursor` 仅能继续读取原来源及原数据库世代。
不同来源之间不承诺全局事件顺序。

`--subject run:RUN_ID` 筛选明确对象；其他类型为 `queue_request`、
`resource_operation`、`resource`，资源事实不要求存在 Run ID。
订阅默认来源为 `project`；云事实使用 `--source resources`，入队派发事实使用
`--source queue`。默认包含保留的匹配事件；`--replay=false` 仅订阅之后的事实。

`notify subscriptions` 分别保留最近成功同步时间、上次来源查询错误的稳定 code 和时间，
之后同步成功也不会抹去旧错误；判断恢复时应比较两类时间。
来源查询失败不是目的地拒绝，也不是 Agent 处理结果。

投递记录分别保留应用派发状态和 SDK 证据。`written` 仅表示已写入传输，
`accepted` 表示目的地明确接收，`processed` 需要处理证据。
Socket 写入或普通 Webhook 接收不能证明 Agent 已处理。
发送结果不确定时保留 `unconfirmed`，不会盲目重发并可能再开启一个 Agent turn。

`notify serve --once --json` 返回一个有限结果；持续的 `notify serve --json`
使用 NDJSON 投递记录，最后输出 end/error 记录。进度不会混入有限 JSON。

## 将固定输入加入单机队列

```bash
loaf queue enable --slots 1
loaf queue submit --name baseline --notify results -- python3 train.py
loaf queue list
loaf queue show QUEUE_REQUEST_ID
```

确认接收前，队列准备独立项目副本并固定命令、环境和输出选择。
后续编辑或同步不会改变它；外部数据集和机器环境仍是外部输入。
`queue submit --dry-run` 仅在本地预览，不连接、不分配 ID 或写入状态。

远端 Coordinator 在本地电脑离线时仍能派发；既有 Supervisor 继续监督已接受 Run。
项目和别名共享同一机器及远端用户的额度，每个请求使用一个槽位。
FIFO 按确认入队的顺序排列，不使用客户端时钟；本版只实现 FIFO。
直接 Run 和外部 SSH 程序不受此协作式队列额度强制隔离。

请求 ID 跟踪入队派发，Run ID 跟踪执行。确认入队不等于程序已启动。
使用 `queue show` 找到关联 Run，再用明确 Run ID 调用 `status`、`logs`、`stop` 和 `pull`。
派发未确认时核对原 Run，不创建第二个替代执行。

```bash
loaf queue pause
loaf queue resume
loaf queue cancel QUEUE_REQUEST_ID
```

暂停只阻止新派发，不停止已接受 Run。取消只在尚未进入派发时成功，不能替代
`loaf stop RUN_ID`。Coordinator 重新启动后可以恢复持久请求。
机器重启后的自动恢复取决于明确配置的受支持服务管理器；启用队列不会自动开机云实例。

## 仅删除选定的取消输入

```bash
loaf queue clean QUEUE_REQUEST_ID --dry-run --json
loaf queue clean QUEUE_REQUEST_ID --yes --json
```

结果包含 `dry_run`、`request_ids` 和队列 `result`。
仅所选且已安全取消请求的准备输入符合条件；不包括活跃 Run、共享数据集和本地下载。
请求身份及 no-start 事实继续保留。普通 `loaf clean RUN_ID` 仍只清理明确确认终态的 Run。

远端磁盘仍保留时，本地断线不会删除远端状态。机器关机时不能运行或派发；
销毁磁盘会失去同盘队列及事件状态，需要保留的事实应在销毁前交付或导出。

## 一个队列订阅关联两个来源

`queue submit --notify results` 在传输源码前，原子登记一个通知订阅，内部同时关联
机器队列与冻结 Run 的项目来源。每个来源分别保存游标；任何一个来源登记失败都会
回滚整个订阅。`notify subscriptions --json` 的 `sources` 展示各来源的同步与错误信息。
准备失败保留原 Queue／Run ID、`stage` 和 `next_action`；未确认派发只查询原身份。
尚未完成准备的请求也可以用原 ID 取消，持久 no-start 记录会阻止延迟准备或启动。

## 处理未确认的投递

先查看事实、冻结接收方、尝试和回执：

```bash
loaf notify delivery show DELIVERY_ID --json
loaf notify delivery reconcile DELIVERY_ID --json
```

`reconcile` 只读核对；当前只有 Claude Channel 提供 Lookup，其他适配器明确报告不支持。
人工确认已接收或决定放弃时：

```bash
loaf notify delivery resolve DELIVERY_ID --as confirmed --json
loaf notify delivery resolve DELIVERY_ID --as abandoned --json
```

人工决定单独保存在 `resolution`，原 SDK 回执不会改写为 processed。
如果确认要承担重复通知、再次触发 Agent 推理及额度消耗的风险，可以逐条授权重发：

```bash
loaf notify delivery resend DELIVERY_ID --allow-duplicate --yes --json
```

该命令只登记一个新 message ID，保留原事实、正文和冻结目的地，并关联原投递。
Bridge 之后才发送。对同一原 ID 重复执行只返回同一个后继，不增加第二条重发。
交互模式省略 `--yes` 会再次确认；非交互必须同时提供两个确认标志。不提供批量重发。

## 暂停、移除与清理通知

```bash
loaf notify subscription show SUBSCRIPTION_ID --json
loaf notify subscription disable SUBSCRIPTION_ID --json
loaf notify subscription enable SUBSCRIPTION_ID --json
loaf notify subscription remove SUBSCRIPTION_ID --json
loaf notify destination remove results --json
```

暂停阻止新采集和新发送；已经 in-flight 的发送无法撤回，结果会给出数量。
恢复沿原游标补取。移除会归档订阅并关闭未发送项，不停止计算，也不抹去未确认的发送。
仍被未移除订阅引用的目的地不能删除，错误会列出引用 ID。

```bash
loaf notify prune --before 2026-10-01T00:00:00Z --dry-run --json
loaf notify prune --before 2026-10-01T00:00:00Z --yes --json
```

默认不自动清理。仅删除指定时间前已结案的投递历史和无引用旧 inbox；去重标记保留，
补取不会重新投递已清理的原消息。不删除 pending、in-flight、未处置 unconfirmed、
云收据、Run 位置或远端事件。

## Webhook 认证与长期运行

```bash
loaf notify destination add results --kind webhook --endpoint https://receiver.example/loaf --bearer-env LOAF_HOOK_TOKEN
```

`LOAF_HOOK_TOKEN` 是由你的现有秘密管理方式提供给 Bridge 的环境变量名。
Loaf 只保存变量名；不保存 token，不从项目配置自动导入目的地。缺失凭据会在发送前失败。
Bearer 必须使用 HTTPS。非 loopback HTTP 需要明确 `--allow-insecure-http`；429 不再视为
可以自动重放的证据。所有 Webhook 都不跟随跳转。

持续 Bridge 没有订阅也保持运行，之后新建的订阅无需重启即可接收。采集、派发和 ACK
核对独立运行，慢来源不阻止已接收消息发送。`--once` 每批有界，可能需要多轮处理积压；
其结果增加 `rejected` 数量，已确认拒绝返回退出 1，未确认投递返回退出 3。

可选 [launchd／systemd 用户服务模板](../../examples/notify-services/README.md) 随包提供，
需自行审查路径并明确安装；Loaf 不自动安装服务。服务重启后继续读取持久状态，Mac 睡眠
时仍无法本地交付；远端磁盘销毁后的事实恢复不在本版保证内。

订阅列表按 ID 分页，默认 100 项；JSON 返回 `subscriptions`、`has_more` 和 `next_after`。
下一页使用 `loaf notify subscriptions --after SUBSCRIPTION_ID --limit 100 --json`。
多来源订阅只在 `sources` 内分别报告成功与错误时间，不能拿一个来源的成功时间解释另一个来源的失败。

`rejected` 统计启用订阅中最后一次被拒绝的投递，包含等待安全重试的项，因此可能与
`pending` 重叠。Bearer 凭据缺失时，一轮命令明确失败且不发 HTTP 请求。
环境变量读取由 CLI 入口显式注入。
