# 命令与最简用法

[首页](../../README.zh-Hans.md) · [使用指南](guide.md) · [English](../en/commands.md)

由 CLI 声明生成。示例中的别名、ID、主机与环境变量引用为占位值。云调用需已配置适用账号与资源条件，变更仍须明确授权和确认，详见[云资源前提](cloud.md)。添加 `--json` 获取非交互结构化结果；原生 `ssh`、`forward` 除外。Agent 后续操作应明确填写任务 ID。

## 全局选项

| 选项 | 类型 | 用途 |
| --- | --- | --- |
| `--lang` | `string` | 界面语言：auto、en 或 zh-Hans。 |
| `--json` | `bool` | 输出 JSON；exec、logs -f 和 cloud watch 使用 NDJSON。 |
| `--trace` | `bool` | 为有限命令报告脱敏 SSH 阶段耗时及连接／重试计数。 |
| `--target` | `string` | 覆盖当前项目的目标别名。 |
| `--help` | `bool` | 显示帮助。 |
| `--version` | `bool` | 显示构建版本。 |

## 实验与项目文件

| 命令 | 最简调用 | 用途 |
| --- | --- | --- |
| `loaf run` | `loaf run -- python3 train.py` | 同步项目并启动可找回的后台任务。 |
| `loaf status` | `loaf status` | 查询指定任务或列出当前项目最近的任务。 |
| `loaf logs` | `loaf logs` | 读取有限日志尾部或持续查看任务日志。 |
| `loaf stop` | `loaf stop` | 停止已保存的任务，并等待确认结果。 |
| `loaf inspect` | `loaf inspect r_example` | 离线查看已保存任务的详情。 |
| `loaf outputs` | `loaf outputs` | 查看任务的输出文件。 |
| `loaf wait` | `loaf wait --tag experiment` | 等待选定任务完成。 |
| `loaf pull` | `loaf pull` | 下载已保存任务的选定输出。 |
| `loaf clean` | `loaf clean r_example --dry-run` | 清理已结束任务的远端数据。 |
| `loaf sync` | `loaf sync --dry-run` | 将当前工作源码同步到受管远端缓存。 |

## 机器与前台操作

| 命令 | 最简调用 | 用途 |
| --- | --- | --- |
| `loaf machine` | `loaf machine` | 管理已保存的机器与当前项目的机器选择。 |
| `loaf machine list` | `loaf machine list` | 列出保存的机器。 |
| `loaf machine show` | `loaf machine show lab` | 查看一台机器的配置。 |
| `loaf machine add` | `loaf machine add lab --ssh lab-gpu` | 保存 SSH 机器别名。 |
| `loaf machine use` | `loaf machine use lab` | 选择当前项目的默认机器。 |
| `loaf machine remove` | `loaf machine remove lab` | 移除本地机器别名，保留任务和远端文件。 |
| `loaf exec` | `loaf exec -- nvidia-smi` | 在所选机器上执行前台命令。 |
| `loaf ssh` | `loaf ssh` | 打开所选机器的原生 SSH 交互终端。 |
| `loaf forward` | `loaf forward 6006` | 通过前台 SSH 连接转发本地回环端口。 |

## 云资源

| 命令 | 最简调用 | 用途 |
| --- | --- | --- |
| `loaf cloud` | `loaf cloud --help` | 管理云资源并接入机器。 |
| `loaf cloud login` | `loaf cloud login main` | 通过 AutoDL 主账号或子账号密码登录 |
| `loaf cloud logout` | `loaf cloud logout main` | 清除所选账号保存的会话和登录摘要 |
| `loaf cloud list` | `loaf cloud list` | 分页查看此账号的实例 |
| `loaf cloud info` | `loaf cloud info instance-id` | 查看实例信息，不暴露凭据 |
| `loaf cloud credentials` | `loaf cloud credentials instance-id` | 获取当前 SSH 连接信息，默认隐藏密码 |
| `loaf cloud start` | `loaf cloud start instance-id` | 明确授权后启动整个已有实例 |
| `loaf cloud stop` | `loaf cloud stop instance-id` | 关闭整个实例，而不只是 Loaf 实验 |
| `loaf cloud search` | `loaf cloud search` | 只读查询一次库存并筛选排序 |
| `loaf cloud watch` | `loaf cloud watch --wait 10m` | 前台只读监测库存，不租用实例 |
| `loaf cloud acquire` | `loaf cloud acquire gpu` | 租用最多一台实例并接入 SSH 机器 |
| `loaf cloud bind` | `loaf cloud bind gpu --instance instance-id` | 绑定已有实例并验证 SSH 身份认证 |
| `loaf cloud requests` | `loaf cloud requests` | 查看本地租用及开关机操作凭据 |
| `loaf cloud reconcile` | `loaf cloud reconcile request-id --instance instance-id` | 核对未确认请求，不重放创建或开关机操作 |
| `loaf cloud account` | `loaf cloud account` | 管理用户私有的云账号凭据引用 |
| `loaf cloud account list` | `loaf cloud account list` | 列出账号引用，不读取令牌 |
| `loaf cloud account add` | `loaf cloud account add main --token-env AUTODL_TOKEN` | 保存平台产品及令牌环境变量引用 |
| `loaf cloud account remove` | `loaf cloud account remove main` | 删除账号别名，保留已有实例绑定 |

## 诊断与帮助

| 命令 | 最简调用 | 用途 |
| --- | --- | --- |
| `loaf check` | `loaf check` | 检查连接与远端基础环境。 |
| `loaf version` | `loaf version` | 显示构建与协议版本、源码提交和平台。 |
| `loaf help` | `loaf help run` | 显示命令帮助。 |

## 参数与条件

使用 `loaf help COMMAND` 查看参数描述、内置默认值、范围及示例；`loaf help COMMAND --json` 增加结构化参数元数据。清单与帮助来自同一命令声明。[参数构造](guide.md#准确构造参数)说明原样 argv、重复选项、CSV、false/零/空值和 Shell 边界。[配置参考](configuration.md)解释生效优先级，[JSON](json.md)说明结果与错误。

`status` 不带 ID 时列出最近任务；`logs`、`stop`、`outputs`、`pull` 不带 ID 时选择最近已提交或未确认任务。`wait` 必须明确 ID 或标签，`clean` 必须明确 ID。其他常用调用：

```bash
loaf run
loaf status ID
loaf logs ID -f
loaf wait ID1 ID2
loaf pull ID1 ID2 --into ./comparison
loaf check --targets a,b
loaf cloud acquire gpu-copy --clone-from old-gpu
loaf cloud bind gpu --request REQUEST
loaf cloud requests REQUEST
```
