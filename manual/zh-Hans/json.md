# 读取 Loaf 的结构化结果

[首页](../../README.zh-Hans.md) · [命令参考](commands.md) · [English](../en/json.md)

## 先判断操作，再判断实验

有限命令加 `--json` 后，stdout 只输出一个 JSON 对象：

```json
{"schema_version":1,"ok":true,"data":{}}
```

`data` 的形状由命令决定，不是统一的空对象。失败时 `error` 包含稳定的 `code`
和可本地化的 `message`、可选 `hint`。失败结果仍可能保留有用的 `data`，
不能因为进程退出码非零就丢弃它。无结果的 `data`、无错误的 `error` 会省略。
不适用字段省略；读取方应容忍后续增加字段。

GPU 表单与就地确认使用显式 `-i / --interactive`；登录也接受自己的 `--prompt / --verify`。
`--json` 不进入提示流程，不能与 `-i` 合用；仅有终端不会自动开启表单。
查询时优先明确使用 `machine list`、
`queue list`、`notify deliveries`、`events list`；裸 `loaf --json` 返回离线帮助。
完整调用直接执行，不要求先经过菜单或单独预览。

| 操作退出码 | 含义 |
| --- | --- |
| 0 | CLI 操作成功，包括成功查到失败实验 |
| 1 | 其他操作失败，包括部分查询或下载失败 |
| 2 | 参数或配置错误 |
| 3 | 提交、停止、前台执行、清理、云变更、队列变更或 Agent 投递未确认 |

任务结果包含 `run_id`、`version`、`target`、`remote_dir`、`status`、`created_at`
和可为 `null` 的 `exit_code`。提交确认、阶段、结束时间、信号、原因、标签、日志、
查询或保存失败、观察时间和清理信息按情况提供。
软件 `version`、JSON `schema_version` 与远端记录格式分别管理。

- `status`：`starting`、`running`、`succeeded`、`failed`、`stopped`、`unknown`。
- `submission`：可用时为 `preparing`、`uncertain`、`submitted`。
  后续观察失败不能推翻已经确认的接管。
- `exit_code: null`：尚无已确认的进程退出结果；数字零是真实退出码。
- `query_error`：未能观察此任务，不是训练失败。
- `state_error`：本地保存或游标写入失败；已验证的远端状态、退出结果仍有效。
- `last_observation`：带时间的历史公开事实，不是实时状态。
  `last_query_failure` 单独表示过去一次查询失败。

时间使用带时区的 RFC3339，比较时应按时刻而非显示文字比较。
毫秒字段的单位为 ms；日志偏移和文件大小按字节计。
价格用十进制字符串表达，CNY 计算费用按整实例每小时，存储费用另按天计。
显存按单卡；命令行内存下限使用 GiB，平台磁盘扩容使用 GB。

## 各命令的数据位置

| 命令 | 有限结果的 `data` |
| --- | --- |
| `run`、`status ID`、`stop`、`logs` | 任务结果；有限日志在 `data.log` |
| `status`、`status --local` | `runs`、`total`、`eligible`、`examined`、`matched`、`unresolved`、`state_errors`、`limit`、`truncated`、`filters`；离线增加 `source: "local"` |
| `inspect ID`、`status ID --local` | 任务字段及来源、版本、兼容性、项目/实例身份、冻结输出 |
| `run --dry-run` | 归一化 argv、源码计划、输出/下载策略、配置来源及环境变量名 |
| `machine`、`machine list/show` | `targets` 数组和 `project` 选择；改名不改变 JSON 结构 |
| `machine add/use/remove` | 保存、选择或删除别名的结果，不代表远端执行 |
| `sync` | 生效策略与选择/传输结果；dry-run 只选择本地文件 |
| `outputs` | 冻结根路径、条目、缺失根及有界覆盖范围 |
| `pull` | 任务、机器、目的地、所选/已下载/缺失路径、策略及可选 `changed_files` |
| 批量 `pull` | `runs[].result` 与可选 `runs[].error`；选中/完成/失败/未检查数 |
| `check` | 单目标报告；批量使用 `checks[]` 和覆盖计数 |
| `wait` | 全部选中任务及 completed/pending/query_errors/state_errors/examined/unexamined/interrupted/timed_out |
| `clean` | `runs[]` 的逐任务清理结果及错误码；执行结果另行保留 |
| `abandon` | `dry_run`、`runs[]` 中的 run、path、eligible 及可选 no_start_at、cleaned_at、error_code、state_error |
| `events list` | 绑定来源的 events、source_id、next_cursor、has_more；事件保留原来源身份 |
| `notify destination list`、`notify subscriptions`、`notify deliveries` | 安全的目的地、订阅或投递行；派发状态与 SDK 证据分开 |
| `notify bind NAME --current` | 只读验证当前线程并保存后的安全 destination 信息，以及 verified_at 时间 |
| `notify serve --once` | received/attempted/accepted/unconfirmed/pending/rejected 计数 |
| `queue submit` | 队列 request、可选 Run 观察或离线 preview；入队不代表执行接受 |
| 其他队列查询/控制 | request(s)、共享额度 status 或明确已删除的 cleaned ID |
| `queue clean` | 明确的 dry_run、request_ids 和队列 result |
| `cloud search` | 冻结计划、匹配、拒绝、观察时间和分页覆盖 |
| `cloud catalog gpu/region/image` | 账号／产品、规范目录项、examined/complete 覆盖与观察时间；地区项可带 `data_center` 分组和 `public` 公开用途标记，缺失表示未知 |
| `cloud profile list/show` | 模板名称／作用域及可选的合并后计划、字段来源 |
| `cloud acquire --dry-run` | 本地账号／目标／计划、字段来源、待实时校验项及可选的克隆继承项 |
| `cloud acquire/bind` | 按阶段返回计划/收据/绑定，失败仍保留已知请求及实例 ID |
| `cloud list/info/requests` | 有界实例页、一个实例、收据列表或指定收据 |
| `cloud start/stop/reconcile` | 保存的资源身份/收据；接受请求不代表电源操作完成 |
| `cloud account/login/logout/credentials` | 账号/登录结果或连接信息；默认隐藏密码 |
| `version`、`--version` | 软件/构建/记录格式版本、平台、Go 版本、源码提交及可空的修改状态 |

列表保留部分成功。历史查询的 `limit` 限制检查候选数，而非保证匹配数；
无法确认的行仍保留。有界结果为空不能证明没有失败任务或没有平台库存。
`OUTPUT_MISSING` 时先检查已下载与缺失路径；未确认传输完成时不提供 `changed_files`。

放弃准备结果中的 `no_start_at` 证明该准备任务不能再启动，`cleaned_at` 单独证明任务数据
已回收。`ABANDON_UNCONFIRMED` 的操作退出码为 3，保留已经确认的字段；应使用相同的
明确 ID 重试。本地 `state_error` 不抹掉远端事实。已保存任务在可选的 `abandonment` 中
提供 requested_at、no_start_at、cleaned_at，与 submission、status、exit_code 分开。
`run.preparation_abandoned` 是公开生命周期事件，不代表训练失败或停止结果。

## 读取参数元数据与错误位置

```bash
loaf help run --json
```

保留的 `data.help` 是人类帮助文本，新增：

- `command`：完整规范命令路径。
- `arguments`、`flags`：名称、类型、本地化描述、必填性、可重复性，及可选的
  `variadic`、`list_format`、静态 `default`、`configurable` 和 `config_key`。
- `commands`：子命令名与本地化描述。
- `examples`：包含 `loaf` 可执行文件名的 argv 数组。

参数还可提供来自编译后 grammar 的 `group`、`enum`、`requires`、`conflicts_with`、
`exactly_one_of`、`applicable_when` 关系注解。人类帮助先显示示例，按组区分操作选项和
继承的全局选项。注解描述输入关系，项目或模板实际生效值仍须本地校验。

`required` 描述解析器的必填规则，不代表全部业务前提。例如 machine add/use/show/remove
允许省略名称以便人类交互补齐，但不带 `-i` 的调用即使有终端，也会对这个缺项返回 `BAD_CONFIG`。

列表形式为 `repeat` 或 `csv`。静态默认值不等于当前项目/用户的生效值；
提交的实际配置来源使用 `run --dry-run`。
命令改名为 `status` 后，原配置键 `[ps].limit` 仍保留。

输入错误可增加 `error.reason`、`error.argument` 和 argv 形式的 `error.help_command`。
配置错误可提供 `error.location` 的文件、字段及已知的一基行列；未知行列省略。
原因区分未知命令/选项、缺参、类型或值错误、越界、冲突、配置错误和旧命令迁移。
不输出原始 argv、配置片段或秘密值。
版本不匹配另带 `expected_version` 与 `actual_version`。

出现 `error.issues[]` 时，其中的 `argument`/`reason` 一次列出已知的本地语义问题。
修正所列问题后再调用；这不表示已执行远端检查。语法错误仍保留解析器的单项诊断。
例如，不提供输入的 `machine add --json` 会在 `issues` 同时列出 `name` 和 `--ssh`，
主错误参数仍为 `name`。
无关配置损坏时仍可使用 help/version；显式无效的 `--lang` 仍需修正。

## 保持建议 argv 的含义

可选的 `next_actions` 条目包含 `kind`、以 `loaf` 开头的 `command` argv 数组及
`requires_confirmation`。直接向进程 API 传递数组，不按空白拆分，也不隐式交给 Shell。
追加 Loaf 输出选项时，应放在 `--` 前面，而非实验程序的字面参数之后。

建议保留明确对象 ID 和公开输入，不包含凭据、私有环境变量值或 `--yes`。
缺失的私有输入须明确提供；建议本身及 `requires_confirmation: false` 都不授予新权限。
不要从公共任务详情拼装历史重跑，也不要为没有预览合同的命令添加 `--dry-run`。
建议动作取决于当前操作及观察结果；缺少动作不意味着应猜测一个。

`run` 和单任务实时 `status` 也提供建议：查看准备记录、查询同一未确认 Run ID、
持续读取运行中任务的日志，或取回成功任务的输出。人类输出使用相同命令。
已回收或正在删除的数据不会建议日志／下载操作。例如成功任务可返回
`{"kind":"pull","command":["loaf","pull","r_ID"],"requires_confirmation":false}`。

## 不解析提示文字也能跟进云操作

云 envelope 可增加 `context`：操作、账号／产品、目标、模式（`select` 或 `auto`）、
阶段、请求 ID 与实例 ID；失败仍保留适用值。原有按阶段变化的 `data` 及键名不变，
恢复结果和成功的预览结果可以提供上述建议 argv。

使用 `cloud bind --request ID --json` 继续接入已知租用结果。未确认请求先检查／核对，不重跑 acquire。更改模板或账号默认值不重解释已保存收据。

默认 `--json` 不输出进度。`cloud acquire/start/stop/bind --progress=json --json` 显式在 **stderr** 输出 NDJSON：`schema_version: 1`、`type: "progress"`、`operation`、`stage`，以及适用的 `request_id`、`instance`、`observation`、`plan`、`poll_count`、`allocation_attempts`、`elapsed_ms`、`next_check_at`、`code`。计数为轮询周期和进入申请尝试的次数（包括最后的只读复查），不是 HTTP 请求数或扣费次数；创建前即时复查可使一轮包含多个只读请求。每行是进度对象，不是有限结果 envelope。完成／错误以 stdout 的唯一 envelope 为准；stderr EOF 或 `created` 事件不证明 SSH 就绪。`NO_STOCK` 与临时读取重试分开。

持续获取使用 `plan.continuous: true`，保存的 `deadline` 为零 RFC3339 时间，不表示有限截止；`wait_timeout` 保留配置值，但不限制该显式模式。普通有限等待及 `--wait 0s` 保持原有语义。

## 逐行消费 NDJSON

| 命令 | 流式数据 |
| --- | --- |
| `logs ID -f --json` | `type: "chunk"` / `"end"`，任务在 `run`，日志在 `run.log` |
| `exec --json -- PROGRAM` | chunk 包含 type、stream、offset、encoding、data；最终 end/error 带目标及执行结果 |
| `cloud watch --wait 10m --json` | 变化/心跳观察及最终 end 或 error |
| 不带 `--once` 的 `notify serve --json` | type 为 delivery，携带 delivery 行；最终 end/error 携带 result |

每行沿用版本化 envelope。早期错误可能是唯一一行，不应假定先有 chunk。
错误携带普通 `error` 和 `data.type: "error"`；没有最终事件的 EOF 不证明成功。
`ssh`、`forward` 不支持 JSON，保留原生终端/隧道行为。
`notify channel` 的标准输入/输出仅承载原生 MCP，不使用 Loaf envelope 或 NDJSON。
传入 `--json`、`--trace` 时退出码为 2，诊断写入 stderr，stdout 留空；
仍可使用 `help notify channel --json` 查看结构化帮助。

日志每块最多 256 KiB，`truncated` 表示前面内容被省略。
偏移按源日志字节而非字符计。有限游标位于 `data.log.cursor`，
流式游标位于 `data.run.log.cursor`。
人类终端仅在写成功后保存；JSON/管道需在处理完后，用离线命令
`logs ID --resume-file FILE --ack-cursor CURSOR --json` 明确确认。

`exec` 每块最多 32 KiB，stdout/stderr 分别计算字节偏移。
`encoding` 为 `base64` 时应解码，不能假设任意程序输出都是 UTF-8。
已确认的非零命令退出为 `EXEC_FAILED`；SSH 255 或取消可能为
`EXEC_UNCONFIRMED`，不能自动重放。

## 完整结果示例

以下虚构 ID、路径和构建版本用于说明完整公开任务结果，文件由契约测试解码：

- [实验成功](../examples/succeeded.json)：CLI 退出 0。
- [实验失败但查询成功](../examples/training-failed.json)：CLI 退出 0，训练退出 7。
- [已接管但观察失败](../examples/accepted-unreachable.json)：CLI 退出 1，保留原 ID。
- [部分历史查询成功](../examples/partial-history.json)：CLI 退出 1，保留两行。
- [远端结果已验证但本地保存失败](../examples/state-error.json)：CLI 退出 2，保留已验证结果。

通知订阅返回 `status` 与逐绑定 `sources`。delivery show 返回安全的冻结正文、尝试、接收方元数据及 parent/resolution 关联；不返回私有 endpoint 或凭据值。人工处置不改写 SDK 回执。

`notify subscriptions` 返回分页对象 `subscriptions`、`has_more` 和可选 `next_after`，
使用 `--after`、`--limit` 继续。多来源订阅仅分别返回来源诊断，不混合来源时间判断恢复。


云接入诊断可增加 `error.failure_reason`；进度可包含 `probe_attempts`、`failure_kind`、`failure_reason`，均为安全分类，不包含 SSH 原始诊断。库存窗口可包含 `scan_offset`、`next_offset`、`has_more`；后页不代表完整库存。成功的离线获取预览在 `next_actions[].command` 提供规范生效 argv，不含 `--yes` 或凭据。`cloud profile save/remove` 沿用模板结果结构。
