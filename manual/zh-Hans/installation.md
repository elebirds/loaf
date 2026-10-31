# 安装 Loaf

[首页](../../README.zh-Hans.md) · [命令参考](commands.md) · [English](../en/installation.md)

## 一条命令安装或更新

macOS、Linux 或 WSL：

```sh
curl -fsSL https://raw.githubusercontent.com/elebirds/loaf/main/install.sh | sh
```

原生 Windows，在 PowerShell 中执行：

```powershell
irm https://raw.githubusercontent.com/elebirds/loaf/main/install.ps1 | iex
```

安装器默认选择最新公开的 GitHub Release，**包括 RC**；验证下载校验和后安装完整包。
更新时再次执行同一命令，直接覆盖安装目录中的文件，不保留旧二进制。
Loaf 配置、凭据、任务数据和远端进程不变。不需要管理员权限或 Go 编译器。
原生 Windows 仍为预览目标。

Unix 默认安装目录为 `~/.local/share/loaf/cli`，Windows 为
`%LOCALAPPDATA%\Loaf\bin`。目录内包含 `loaf`／`loaf.exe`、手册、示例、Skill 和许可证。
安装器自动将该目录加入用户 PATH，重复执行不重复添加。
Unix 下重开终端，或执行安装器打印的激活命令；PowerShell 同时更新当前会话的 PATH。

可选环境变量：`LOAF_INSTALL_DIR` 指定绝对安装目录，`LOAF_VERSION` 指定准确发行标签，
`LOAF_NO_MODIFY_PATH=1` 跳过 Shell 启动文件／用户 PATH 修改。
默认使用时不需要设置这些变量，直接安装最新发布即可。
Unix 脚本按用户 Shell 配置 Bash、Zsh、Fish 或 POSIX 登录 profile；也可先在仓库中阅读脚本再执行。

远端工作流仍需要 OpenSSH 和 rsync，安装器不自动安装系统依赖；
Windows 继续按下文配置 [MSYS2](#原生-windows)。

## 手动安装

从 [Releases](https://github.com/elebirds/loaf/releases) 下载匹配平台的压缩包及校验文件。文件名中，`darwin` 表示 macOS，`linux` 表示 Linux，`arm64` 对应 Apple Silicon/ARM64，`amd64` 对应 Intel/AMD x86-64。将压缩包解压到新目录。

可用命令以压缩包内的手册为准。仓库 `main` 分支可能包含公开下载版尚未提供的功能；
`VERSION` 记录目标版本，不表示该版本已经发布。试用 `main` 请按下文从源码构建。
正式 `1.x` 更新继续访问受支持的旧正式 `1.x` 工作。开发版、`0.x` 与 RC 状态不导入，
安装器也不保留旧二进制；这些版本所建任务的管理限制见[升级说明](upgrading.md)。

压缩包包含一个可执行文件、双语 README、Skill、示例、`BUILD.json` 及第三方许可证原文。解压后，在该目录执行：

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 loaf "$HOME/.local/bin/loaf"
"$HOME/.local/bin/loaf" version --json
```

按需将该目录加入 PATH。使用压缩包只需要本地 OpenSSH 和 rsync，不需要 Go。远端需要 Linux、rsync；实验的 Python 环境应单独准备。

压缩包构建目标为 macOS/Linux amd64、arm64，以及原生 Windows x64。
WSL2 Ubuntu x64 使用 Linux amd64 包，远端 Runner 保持 Linux amd64/arm64。
原生 Windows x64 目前为预览目标：完整发行包的 SSH／同步／Run／Pull 流程仍需完成
实际验收，才列入 v1.0 稳定支持；Windows 10 的运行验收也待完成。
原生 Windows ARM64、Windows 远端执行和 UNC 网络共享不在本版范围；交叉构建不等于运行验证。

`BUILD.json` 保存源码提交、工作树修改状态、Go 版本、目标平台和依赖声明清单；`loaf version --json` 提供对应的构建标识，缺少来源信息时如实显示未知，不伪装成干净构建。将 `archive` 设为压缩包的准确文件名、`version` 设为文件名中的软件版本，再在包含压缩包和校验文件的目录中执行：

```bash
# archive 是压缩包的准确文件名
grep -F "  $archive" "loaf_${version}_checksums.txt" | shasum -a 256 -c -
```

公开发布的压缩包附带 GitHub 构建来源证明，可用 `gh attestation verify "$archive" --repo elebirds/loaf` 检查，详见 [GitHub 验证说明](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations#verifying-artifact-attestations-with-the-github-cli)。本地构建包含元数据和下载校验和，不含 GitHub 签名的来源证明。

### 正式版与开发版状态

正式 `1.x` 构建在平台的 Loaf 状态目录下使用 `stable-v1/state.db`，开发版与 RC 使用
`state-v4/state.db`；配置和凭据另行保存。两者都不导入旧存储。正式 `1.x` 更新继续
使用同一个稳定状态目录，不因软件版本或 schema 变化另开空库。
远端项目和队列的边界见[升级说明](upgrading.md)。

### 原生 Windows

使用 `windows_amd64.zip`，将其解压到本地 NTFS 磁盘的新目录。包内包含 `loaf.exe` 和与 Unix 包相同的手册、示例及许可证。可用 PowerShell 的 `Expand-Archive` 解压，然后在目录内运行 `./loaf.exe version --json`。将该目录加入用户 PATH 后，可在 PowerShell 或 CMD 中运行 `loaf`。用 `Get-FileHash -Algorithm SHA256` 计算 ZIP 哈希，与校验文件中的对应记录核对。

安装 [MSYS2](https://www.msys2.org/)，在其终端中执行 `pacman -Syu`；若提示重开终端，重开后继续更新。随后安装配套传输工具：

```bash
pacman -S --needed openssh rsync
```

在 PowerShell 指定安装目录，按实际位置调整：

```powershell
$env:LOAF_MSYS2_ROOT = 'C:\msys64'
loaf version --json
loaf machine add lab --ssh lab-gpu
loaf machine use lab
loaf check --json
```

CMD 使用 `set "LOAF_MSYS2_ROOT=C:\msys64"`。这些设置只对当前终端生效；需要保留时，可在 Windows 用户环境变量设置中保存。未设置该变量时，Loaf 从 PATH 中找到 `rsync.exe`，并要求同目录的 MSYS2 OpenSSH 与 `cygpath.exe`。Loaf 不下载依赖，也不混用 Windows OpenSSH、Git SSH、Cygwin 或 WSL 工具。SSH 别名、密钥、Agent 和跳板机需要在所选 MSYS2 安装中可用。SSH 配置里的路径使用正斜杠，含空格的路径加引号；必要时通过 `ssh -F C:/path/config host` 明确选择配置。

Windows 的每次操作使用独立 SSH 连接，包括云操作。云连接信息始终按已冻结的实例身份刷新；此路径不建立私有 SSH master。密码认证使用私有原生 askpass helper；普通静态 SSH 的认证和主机密钥验证仍由 OpenSSH 负责。

项目、状态和下载目录使用本地 NTFS 磁盘，支持中文、空格和跨盘下载。下载会在传输前拒绝 Windows 保留名称、非法字符与大小写冲突。相对符号链接需要目标文件系统实际具备创建权限；不支持时明确失败，不转成普通文件。原生 Windows 支持事件存储、Bridge、Webhook 通知和远端队列；Unix socket Agent 适配器返回 `DESTINATION_UNSUPPORTED`。

原生 Windows 的私有文件凭据后端已通过测试；Windows Credential Manager 仍需使用隔离 Windows 用户完成实际验收。

### WSL2

在 Ubuntu 内执行 `sudo apt install openssh-client rsync`，然后使用 Linux amd64 包和前面的 Unix 安装步骤。包括 `/mnt/c` 下的项目，都在 WSL 内运行 Linux `loaf`，不要调用 `loaf.exe`。状态与凭据保存在 Linux 主目录，与原生 Windows 分开；不要将 XDG 覆盖指向 Windows 状态库。此路径保留 Linux 的私有 SSH 连接复用。Agent 适配器必须在该 WSL 环境中可访问且通过验证；Windows 桌面会话不会自动变成可用目标。

### 从源码构建

需要 Go 1.27.3、Git、OpenSSH 和 rsync。Go 版本固定在 `mise.toml`；使用 mise 时，请先检查该文件再信任项目配置。

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

构建会内置 Linux amd64、arm64 远端封装，并生成 `dist/loaf`（Windows 为 `dist/loaf.exe`）、平台压缩包和 `loaf_<version>_checksums.txt`。Windows 包为 ZIP，Unix 包为 tar.gz。默认构建标识由 `VERSION` 中的目标版本加开发提交和时间戳组成；显式设置 `VERSION` 环境变量可选择准确的软件版本。可用 `LOAF_BUILD_TARGETS` 选择其他目标，例如 `linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64`。Windows 下在 Git Bash 或 MSYS2 Bash 中运行同一脚本，并确保 PATH 使用项目指定版本的 Windows Go；race 检查还需要受支持的 C 编译器，例如 MSYS2 UCRT64 GCC（`mingw-w64-ucrt-x86_64-gcc`）。
