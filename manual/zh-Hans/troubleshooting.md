# 恢复同一次操作

[首页](../../README.zh-Hans.md) · [命令参考](commands.md) · [English](../en/troubleshooting.md)

## 连接前先修正本地输入

```bash
loaf help machine add --json
loaf help run --json
```

通过 `error.argument` 与 `error.reason` 定位问题；如果提供 `error.issues`，可一次修正
列出的本地问题。`error.help_command` 是对应帮助页的 argv，不会重放失败的变更操作。
无关配置错误不影响 help/version；仅查看语法无需登录账号、读取凭据或检查连接。

不加 `-i` 时，即使有 PTY 也不会通过表单补齐缺项；请在命令或支持的配置中提供所需值，
或显式加 `-i` 打开供人使用的向导。
有效的空值、零、false 不同于省略：`--wait 0s` 表示不等待，`status --limit 0` 则越界。
输入错误省略参数值和配置片段，不要为补充诊断而粘贴私有输入。

## 诊断连接，避免重放操作

```bash
loaf check --trace --json
loaf pull <任务ID> --trace --json
```

`--trace` 为有限 JSON 结果增加有界 `trace` 对象，提供阶段耗时、连接解析、成功建立的私有主连接、重连和只读重试计数。重试事件给出 `private_master_exit` 等稳定原因；操作恢复成功后仍保留首次失败的诊断。普通模式将诊断写入 stderr。收集须显式启用，不记录命令、端点、请求体、密码、token 或环境变量值。

耗时可能嵌套，不应相加。私有主连接计数适用于云 runner 连接，不代表用户静态 SSH 的连接共享；诊断涵盖 SSH 工作流及其云连接查询，不追踪无关的云目录／租用 HTTP 操作。明细最多 512 条，以 `truncated: true` 标记截断，计数仍完整。`check --trace` 保持远端只读，除非另外显式指定 `--runner`。持续流、前台 `exec`、交互终端、隧道及云 watch 不支持 trace。

连接失败会提供稳定的 `error.stage`，例如 `cloud_connection`、`ssh_connect`、`runner_verify`、`sync`、`launch`、`observe`、`pull`，并保留原错误码及可用的 SSH／平台诊断。封装版本验证失败本身不代表可执行文件损坏。

根据已有连接证据选择下一步：

- 未知主机密钥：先向机器管理员核实指纹，再通过原生 `loaf ssh --target lab` 明确信任。
- 主机密钥变化：先核实变更原因，不自动删除 `known_hosts` 记录或接受替换密钥。
- 认证或配置问题：用 `loaf machine show lab` 核对目标，再检查对应 SSH 用户、密钥／agent
  或配置，不公开原始 SSH 诊断。
- 超时或断线：可以重新 `check` 收集证据；若前台命令返回 `EXEC_UNCONFIRMED`，仍需先核实
  远端执行情况，不能直接重放。

把 `lab` 换成实际保存的别名。分享诊断时导出白名单化的 `check --report` 文件；
普通 `check --json` 可能包含工具路径，不等同于脱敏报告。

有限只读探测、状态／日志查询、输出检查和下载，在识别到临时传输故障后最多追加一次尝试，退避短暂且可取消。认证、主机密钥、配置和路径错误不重试；默认日志流已输出部分内容后不重启；只有显式 `logs -f --reconnect` 或 `--resume` 才启用游标恢复。源码同步、封装上传、提交、停止、清理和前台命令不自动重放。下载保留已完成文件，并沿用原任务及目的地。

源码缺失时，用 `loaf sync --dry-run` 检查 Git 和 Loaf 的排除规则；项目位于上级仓库的忽略目录中时，仍受该规则约束。命令失败时先查看 `loaf logs`：接管成功后仍可能因解释器、依赖或输入缺失而失败。提交或停止未确认时，应查询原 ID，而非另开替代任务。
