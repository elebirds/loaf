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

| 操作退出码 | 含义 |
| --- | --- |
| 0 | CLI 操作成功，包括成功查到失败实验 |
| 1 | 其他操作失败，包括部分查询或下载失败 |
| 2 | 参数或配置错误 |
| 3 | 提交、停止、前台执行、清理或云变更未确认 |

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
| `cloud search` | 冻结计划、匹配、拒绝、观察时间和分页覆盖 |
| `cloud acquire/bind` | 按阶段返回计划/收据/绑定，失败仍保留已知请求及实例 ID |
| `cloud list/info/requests` | 有界实例页、一个实例、收据列表或指定收据 |
| `cloud start/stop/reconcile` | 保存的资源身份/收据；接受请求不代表电源操作完成 |
| `cloud account/login/logout/credentials` | 账号/登录结果或连接信息；默认隐藏密码 |
| `version`、`--version` | 软件/构建/记录格式版本、平台、Go 版本、源码提交及可空的修改状态 |

列表保留部分成功。历史查询的 `limit` 限制检查候选数，而非保证匹配数；
无法确认的行仍保留。有界结果为空不能证明没有失败任务或没有平台库存。
`OUTPUT_MISSING` 时先检查已下载与缺失路径；未确认传输完成时不提供 `changed_files`。

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

列表形式为 `repeat` 或 `csv`。静态默认值不等于当前项目/用户的生效值；
提交的实际配置来源使用 `run --dry-run`。
命令改名为 `status` 后，原配置键 `[ps].limit` 仍保留。

输入错误可增加 `error.reason`、`error.argument` 和 argv 形式的 `error.help_command`。
配置错误可提供 `error.location` 的文件、字段及已知的一基行列；未知行列省略。
原因区分未知命令/选项、缺参、类型或值错误、越界、冲突、配置错误和旧命令迁移。
不输出原始 argv、配置片段或秘密值。
版本不匹配另带 `expected_version` 与 `actual_version`。

## 逐行消费 NDJSON

| 命令 | 流式数据 |
| --- | --- |
| `logs ID -f --json` | `type: "chunk"` / `"end"`，任务在 `run`，日志在 `run.log` |
| `exec --json -- PROGRAM` | chunk 包含 type、stream、offset、encoding、data；最终 end/error 带目标及执行结果 |
| `cloud watch --wait 10m --json` | 变化/心跳观察及最终 end 或 error |

每行沿用版本化 envelope。早期错误可能是唯一一行，不应假定先有 chunk。
错误携带普通 `error` 和 `data.type: "error"`；没有最终事件的 EOF 不证明成功。
`ssh`、`forward` 不支持 JSON，保留原生终端/隧道行为。

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
