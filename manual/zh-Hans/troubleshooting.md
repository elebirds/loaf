# 恢复同一次操作

[首页](../../README.zh-Hans.md) · [命令参考](commands.md) · [English](../en/troubleshooting.md)

## 常见问题

```bash
loaf check --trace --json
loaf pull <任务ID> --trace --json
```

`--trace` 为有限 JSON 结果增加有界 `trace` 对象，提供阶段耗时、连接解析、成功建立的私有主连接、重连和只读重试计数。重试事件给出 `private_master_exit` 等稳定原因；操作恢复成功后仍保留首次失败的诊断。普通模式将诊断写入 stderr。收集须显式启用，不记录命令、端点、请求体、密码、token 或环境变量值。

耗时可能嵌套，不应相加。私有主连接计数适用于云 runner 连接，不代表用户静态 SSH 的连接共享；诊断涵盖 SSH 工作流及其云连接查询，不追踪无关的云目录／租用 HTTP 操作。明细最多 512 条，以 `truncated: true` 标记截断，计数仍完整。`check --trace` 保持远端只读，除非另外显式指定 `--runner`。持续流、前台 `exec`、交互终端、隧道及云 watch 不支持 trace。

### 原生 Windows 与 WSL

Windows 的 `masters_established` 始终为零，这是独立连接策略的正常结果；每次云连接仍使用冻结的实例，修改操作不会自动重放。工具缺失或混用时，在同一 MSYS2 安装中准备 `openssh` 和 `rsync`，将 `LOAF_MSYS2_ROOT` 指向根目录（例如 `C:\msys64`），而不是 `usr\bin`。使用同一安装检查 SSH 配置与认证；配置路径使用正斜杠，含空格时加引号，并保持主机密钥验证。

下载出现 `UNSAFE_PATH`，可能是 Windows 保留名称、末尾空格／句点、大小写冲突、重解析点或不支持的链接。请调整产物名称，或使用 WSL 的 Linux 文件系统；Loaf 不改名，也不覆盖大小写拼写不同的本地名称。状态和凭据使用本地 NTFS 私有 ACL；修正权限时保留被拒绝的状态库，不要删除未确认操作的证据。WSL 使用独立 Linux 状态与凭据。原生 Unix socket Agent 适配器返回 `DESTINATION_UNSUPPORTED`；使用 Webhook 或 WSL 内已验证的适配器，不替换会话或绕过权限。

连接失败会提供稳定的 `error.stage`，例如 `cloud_connection`、`ssh_connect`、`runner_verify`、`sync`、`launch`、`observe`、`pull`，并保留原错误码及可用的 SSH／平台诊断。封装版本验证失败本身不代表可执行文件损坏。

有限只读探测、状态／日志查询、输出检查和下载，在识别到临时传输故障后最多追加一次尝试，退避短暂且可取消。认证、主机密钥、配置和路径错误不重试；默认日志流已输出部分内容后不重启；只有显式 `logs -f --reconnect` 或 `--resume` 才启用游标恢复。源码同步、封装上传、提交、停止、清理和前台命令不自动重放。下载保留已完成文件，并沿用原任务及目的地。

源码缺失时，用 `loaf sync --dry-run` 检查 Git 和 Loaf 的排除规则；项目位于上级仓库的忽略目录中时，仍受该规则约束。命令失败时先查看 `loaf logs`：接管成功后仍可能因解释器、依赖或输入缺失而失败。提交或停止未确认时，应查询原 ID，而非另开替代任务。
