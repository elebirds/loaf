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

v0.6 在 `state-v2` 和 `.loaf-control-v2` 启用全新的 schema-2 SQLite 状态，
不导入 schema-1、旧 JSON 索引或云收据。schema-1 任务保留匹配的 v0.5 CLI，
不原地升级活跃 Supervisor 正在使用的数据库。
使用 `loaf machine use NAME` 重新选择项目机器；新项目身份会在默认远端基目录下
得到新的受管目录。显式 `remote_dir` 应适合新格式，不能接管已有的不兼容项目。

旧任务和未确认云请求使用匹配旧 CLI 处理。新数据库为空不能证明旧付费请求未发生，
再次获取前应先核对原请求。未知格式仍拒绝覆盖。
软件、数据库、wire、事件和独立投递 SDK 分别管理版本。
