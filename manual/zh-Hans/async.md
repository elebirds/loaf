# 接收结果通知，共享一台机器

[首页](../../README.zh-Hans.md) · [使用指南](guide.md) · [命令](commands.md) · [English](../en/async.md)

任务结束后需要交付结果，使用通知；协作项目需要共享机器，使用队列。
直接 Run 和云资源操作不需要经过队列，也能发送通知。

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

Agent 绑定一个明确会话：

```bash
loaf notify destination add assistant --kind codex --session THREAD_ID --endpoint ENDPOINT
```

使用所安装适配器支持的接入端。`codex`、`claude-channel`、显式选择的
`claude-resume` 是不同模式。保存绑定不等于证明可连接或可处理。
Loaf 不猜测最近会话、不静默新建替代会话，也不绕过原工作区和审批要求。
Claude Channel 要求会话开启且平台允许；headless resume 是另一种需要明确选择的执行模式。

`notify destination list` 只显示安全信息，不显示私有连接详情。
独立 Go 投递 SDK 负责明确目的地及投递证据，不负责 Run 调度或训练 Runtime。

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
