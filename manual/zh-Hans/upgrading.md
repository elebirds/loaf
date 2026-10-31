# 升级时保留对已有任务的访问

[首页](../../README.zh-Hans.md) · [安装](installation.md) · [English](../en/upgrading.md)

## 使用安装器更新

再次执行[安装命令](installation.md#一条命令安装或更新)，即可覆盖为最新公开发布，包括 RC。
安装器不保留旧二进制，也不修改配置、凭据、任务数据库或远端进程。
这不会迁移不受支持的开发期历史；下文的正式兼容边界仍然适用。

## 兼容承诺从首个正式 1.0 版本开始

兼容基线是 **`v1.0.0` 正式版**，不包括之前的开发版本。
更新的正式 `1.x` CLI 支持管理较早正式 `1.x` 创建的 Run、队列、云操作收据、
订阅、已接收事件和投递记录。原 Run ID、软件来源版本、机器绑定、输入和输出选择保持不变。

例如，从 `v1.0.0` 更新到后续正式 `1.x` 后，在同一项目中继续使用原 ID：

```bash
loaf status RUN_ID --json
loaf logs RUN_ID
loaf pull RUN_ID
```

停止、终态清理和放弃准备仍需满足原有授权和适用条件。
旧正式 Coordinator 可以继续派发原队列，新兼容 CLI 可查询、控制并提交双方支持的任务。
新功能若需要旧组件没有的能力，仅该功能需要明确升级，不影响原有操作。

软件身份与 JSON、wire、事件及数据库格式分开管理。
`inspect ID` 同时显示任务原 `version`、当前 `client_version`；`compatible` 不再只是两个字符串相等。
部署仍验证版本化路径中的精确内置 helper 身份。
新的控制 helper 不会替换或重启活动 Supervisor，不重发启动，也不改写保存的 Run 绑定。

该承诺只覆盖新正式 `1.x` 客户端管理旧正式 `1.x` 工作，不承诺旧客户端读取新格式、
降级或跨大版本升级。未知格式会被明确拒绝而不覆盖数据。
第三方会话可用性、凭据和远端磁盘保留仍是各自独立的使用条件。

## 正式状态与开发状态分开

正式 `1.x` 使用平台 Loaf 状态目录下固定的 `stable-v1/state.db`。
目录名表示受支持的发行系列，不是 SQLite schema；普通 `1.x` 更新不会创建空白历史。
用 `version --json` 查看实际安装的构建身份。

开发构建和预发布版（含 `v1.0.0-rc.*`）使用 `state-v4/state.db`，不导入历史状态；
保存的开发工作要求匹配的开发软件身份。本地及项目远端库为 schema 4，机器队列为 schema 2。
库内单独记录正式或开发系列，因此把开发库复制到正式目录不会使其变成受支持的正式状态。

项目远端库仍是 `.loaf-control-v2/state.sqlite3`，不随软件版本更换命名空间，
也不让其他 helper 绕过已保留的 no-start 声明。
正式 helper 拒绝开发版项目库；首个正式版使用新的受管远端项目根，保留本地仓库和原远端工作。

机器/远端用户队列仍使用唯一的 `~/.local/share/loaf/queue-v1/state.sqlite3`。
正式版拒绝开发版账本，不在旁边自动新建第二套容量账本。
先用原工具收尾开发队列，在原 Coordinator 退出后明确归档旧账本；或选择没有旧队列的远端身份。
不能删除仍被 Coordinator 使用的账本。

## 从开发版切换到正式版只分界一次

`v0.x`、开发构建和 RC 的历史工作不受支持：不自动导入、转换数据库、提供旧版适配器或改绑任务。
替换唯一的开发 CLI 前，按版本保留原程序，以及管理存量工作所需的目录和收据。
用原工具完成、观察、停止或取回原任务，并核对未确认的付费操作。
正式版历史为空不表示以前的资源申请没有发生。

符合当前 TOML 格式的既有配置仍可读取；不增加历史配置转换器，也不删除凭据。
在目标会话中重新登记正式版通知目的地。不要把开发记录搬进正式库，
也不要替换活动 Supervisor 伪装成兼容。

对明确安装的 Bridge 用户服务，停止旧进程、更新可执行路径后，以同一正式状态目录重启。
游标、去重及投递证据会保留；可能已发送但确认丢失的消息仍为 Unconfirmed，不因升级而重发。
不安装通用远端组件升级器或后台迁移服务。

## 开发期间调整过的命令入口

| 旧调用 | 新调用 |
| --- | --- |
| `loaf add NAME --ssh HOST` | `loaf machine add NAME --ssh HOST` |
| `loaf use NAME` | `loaf machine use NAME` |
| `loaf targets` | `loaf machine` 或 `loaf machine list` |
| `loaf targets NAME` | `loaf machine show NAME` |
| `loaf remove NAME` | `loaf machine remove NAME` |
| `loaf ps [ID]` | `loaf status [ID]` |

旧入口返回 `BAD_CONFIG`、原因 `command_replaced`、操作退出码 2 和替代用法，
不执行原操作。请更新脚本和 Agent 指令，不提供静默转发的兼容别名。
`loaf --version` 与 `loaf version` 使用同一构建结果。

命令整理不重命名 `[ps].limit`，不改变原 JSON 数据键，不转换数据库、
改向已有绑定或改变下载位置。机器列表/详情的 JSON 仍为 `targets` 与 `project`。

`VERSION_MISMATCH` 对不支持的软件配对仍提供 `expected_version` 和 `actual_version`。
普通 runner 操作会在需要时自动部署和验证当前内置控制 helper；只读预览不部署。
这不会导入不支持的历史库，也不授权替换 Supervisor。
