# 构建与开发 Loaf

[首页](../../README.zh-Hans.md) · [命令参考](commands.md) · [English](../en/development.md)

## 架构

Loaf 是拥有两个可执行文件的模块化单体，基础使用不依赖常驻服务或 API Server：

```text
loaf：Kong 命令声明 → 命令适配／呈现
                   → client.Projects / Machines / Resources
                   → run / cloud / 文件选择
                   → OpenSSH / rsync / AutoDL / state / credentials
                                   │
loaf-runner：短时控制请求 ← SSH / rsync
           → 受管项目 + 每 Run 独立 Supervisor
           → 项目 SQLite 控制库 + 普通 payload 文件
```

人和 Agent 共用 CLI 工作流。只有 GPU 获取使用多步 TUI；登录、添加机器和删除确认
使用就地输入，不设置根级大厅或跨命令导航。配置按一次命令读取，资源用例在明确修改
账户或配置后刷新自己的快照。

### 模块职责与依赖方向

- `cmd/loaf` 装配具体依赖和进程取消上下文。`internal/cli` 将声明式参数转成类型化请求，分别呈现有限结果、NDJSON 与原生流。Huh 负责普通输入和默认否确认；登录的用户名、密码、验证码由同一个有界原生输入会话负责。表单与进度使用 stderr，JSON 不进入提示流程。
- `internal/client` 负责完整本地用例。**Projects** 组织项目计划、提交、观察、传输和清理；**Machines** 负责别名、检查及前台机器操作；**Resources** 负责资源计划、授权、获取、绑定和认证。调用者提供明确认证策略及进度接收函数，不传 parser、localizer 或输出模式。`App` 只负责聚合三个服务。
- `internal/run` 拥有运行语义及恢复；纯函数 `ResolveExecution` 为预览和提交统一归一化、校验命令、setup、环境和输出选择，预览只展示环境变量名。`internal/cloud` 拥有资源条件、生命周期意图及小型 provider 能力；`internal/sync` 拥有文件选择与传输规则。接口由真正使用副作用的模块定义，不设置通用 Repository、后端注册中心或 DI 容器。
- `internal/ssh`、`internal/autodl`、`internal/credentials` 负责外部适配。SSH 命令构造与引用集中在传输边界，继续复用系统 OpenSSH 和 rsync 协议。
- `internal/task` 保存冻结任务位置、导航标签和公开观察等值类型。`internal/config` 读取 TOML、发现项目、合并偏好及校验值。`internal/state/local`、`internal/state/receipts`、`internal/state/remote` 通过 `database/sql` 和 `modernc.org/sqlite` 执行明确 SQL，不使用 ORM。本地保存意图与位置，远端保存执行事实。
- `internal/remote` 负责受管目录、进程身份、进程组终止、回收和终态发布，只依赖 wire 类型及系统能力，不依赖 CLI、本地配置或云账户。`cmd/loaf-runner` 派发短时控制请求，或启动逐任务 Supervisor。

### 提交、异常恢复与清理

应用层生成计划、检查先决条件，先将任务身份和冻结绑定保存为 `preparing`。随后同步工作文件，Runner 排他认领该 ID 并准备独立工作副本。客户端在尝试一次启动前保存 `uncertain`，确认接管后再记录 `submitted`。响应丢失后观察**同一个 ID**，不会另生成 ID 重复提交。提交确认、执行状态、本次观察失败和本地保存失败分别保留；取消等待不等于停止已接管任务。

数据库事务只覆盖短时元数据更新，不跨 SSH、HTTP、文件复制或任务运行。原有 OS 锁继续管理源码接收／复制、阶段准入、进程存活及 payload 访问。清理仅删除选定终态任务的 payload 和私有执行数据，持久保留 claim 与公开删除摘要，防止延迟启动重新执行；本地清理可用性与执行终态独立。

## 产品边界

直接运行已经包含准备、同步和提交，不要求用户另走预检流程。GPU 表单与命令参数调用
同一个 Resources 用例；新行为放在所属用例，不放进菜单导航或第二套命令模型。
开发时检查被改动的路径及其具体副作用；集成复用现有共享检查和隔离入口，不另建门禁。

## 开发

```bash
go run ./scripts/format.go    # Go 代码使用四空格格式化
go tool depguard ./...       # 包依赖边界检查
bash scripts/check.sh        # 格式、依赖、单元、静态、竞态及漏洞检查
bash scripts/integration.sh  # SSH、Linux 进程与凭据测试，需要 Docker
bash scripts/integration.sh -run TestProjectRunWaitSetupAndRecovery
bash scripts/integration.sh -run TestLinuxCloudCredentialStores
```

共享检查脚本在格式检查后运行固定版本的独立 depguard 工具，用少量禁止依赖规则保护 CLI、用例和远端边界，不为所有包维护依赖白名单。

集成环境使用临时密钥和 CPU 脚本，验证发行包中的 macOS/宿主及 Linux 客户端原生 rsync、镜像边界、中断同步恢复、并发启动、同版本封装部署、跳板连接、断线执行、退出结果、进程组停止、有界等待、日志取消和增量下载。前台操作覆盖即时输出、字面 argv、PTY 登录、独立隧道和取消；历史及清理覆盖原目标绑定、明确删除范围、传输锁、重试和终态保留，不接触科研服务器。构建目标包括 macOS/Linux 客户端、Windows x64 ZIP 客户端及 Linux amd64、arm64 封装；交叉构建与实际运行分别验证。原生 Windows 固定使用配套的 MSYS2 OpenSSH/rsync 和独立连接，Linux 与 WSL2 保留私有连接复用。本地故障注入使用 Go 子进程 helper，控制台测试在 Unix 使用 PTY、在 Windows 使用测试独占的 ConPTY。Windows 单元测试覆盖私有 ACL、WAL 权限继承、跨进程锁、替换失败、数据库恢复、密码取消及 Job Object 进程归属。将 `LOAF_WINDOWS_TEST_VOLUME` 指向第二块本地 NTFS 盘可验证跨盘下载。在 WSL2 中，将 `LOAF_WSL_PROJECT_BASE` 指向 `/mnt/c` 下已有测试目录，可验证挂载项目与独立 Linux 私有状态目录。Windows 10 实际验收仍待独立测试环境。

云测试通过模拟 API 验证两种 AutoDL 产品，并使用隔离 SSH 服务器验证原生密码认证、Provider 首次主机密钥自动接受、已知密钥变化拒绝、固定实例绑定及结果下载。跨进程测试覆盖会话过期、保存摘要后的重新认证、密码变更、登出后重新登录、凭据后端选择，以及响应丢失或客户端被杀后的恢复，确保不会重复租用或重放开关机请求。

Linux 凭据 fixture 在容器私有会话中以普通用户运行 Secret Service 钥匙环，验证原生读写、从文件切换至钥匙环的登录生命周期，以及会话总线不可用的情况；不会挂载宿主钥匙环。平台 HTTP 仍由模拟服务提供，这些测试不能证明真实账号的接口可用性、当前库存或实际计费；真实租用验收需要单独授权账号及预算。

可选的单目标 FIFO 队列使用显式启用的远端 Coordinator，直接运行不依赖它。Loaf 不提供实验重试、抢占、多节点调度、自动环境修复或自动云关机；云租用仍须显式触发。

维护者显式推送 `vX.Y.Z` 或预发布标签后，发布工作流才会运行同一套 CI、SSH 检查和 `scripts/build.sh`，为压缩包生成来源证明并创建 Release。普通分支和 PR 没有发布权限。
