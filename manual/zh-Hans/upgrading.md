# 升级时保留对已有任务的访问

[首页](../../README.zh-Hans.md) · [安装](installation.md) · [English](../en/upgrading.md)

## 命令入口调整

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

## 保留管理原任务所需的 CLI

软件身份必须匹配保存任务，与 JSON、wire、数据库格式版本分别管理。
替换唯一可执行文件前，按版本保留原 CLI，用它查询、停止或取回原任务。
`inspect ID` 可以离线查看所需版本；新 CLI 不迁移记录、替换运行中的监督器或下载旧程序。

`VERSION_MISMATCH` 提供 `expected_version` 和 `actual_version`。
已有任务应使用匹配 CLI。当前版本绑定缺少相应 helper 是另一种情况：
普通 runner 操作会自动部署并验证内置 helper；只读预览不部署。

## v0.6 的状态边界

当前开发版使用全新的 `state-v3` 本地 schema 3，不导入旧任务位置、订阅、
收据或目的地。请在目标 Codex 会话内重新绑定 `here`。旧数据库和凭据不会删除。
新远端项目使用 `.loaf-control-v2` 的 schema 3，保存放弃准备后的持久 no-start 收据。
已有 schema 2 项目会被明确拒绝而不转换；使用本开发版时另选新的远端项目目录，
保留原目录及其匹配的 CLI。不热升级活动 Supervisor。
本开发版不支持历史格式与接口。新本地库为空不表示以前不确定的云操作没有发生；
实际再次获取资源前，仍须核对原付费操作并取得明确授权。
