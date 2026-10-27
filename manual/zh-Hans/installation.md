# 安装 Loaf

[首页](../../README.zh-Hans.md) · [命令参考](commands.md) · [English](../en/installation.md)

从 [Releases](https://github.com/elebirds/loaf/releases) 下载匹配平台的压缩包及校验文件。文件名中，`darwin` 表示 macOS，`linux` 表示 Linux，`arm64` 对应 Apple Silicon/ARM64，`amd64` 对应 Intel/AMD x86-64。将压缩包解压到新目录。

压缩包包含一个可执行文件、双语 README、Skill、示例、`BUILD.json` 及第三方许可证原文。解压后，在该目录执行：

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 loaf "$HOME/.local/bin/loaf"
"$HOME/.local/bin/loaf" version --json
```

按需将该目录加入 PATH。使用压缩包只需要本地 OpenSSH 和 rsync，不需要 Go。远端需要 Linux、rsync；实验的 Python 环境应单独准备。

客户端构建目标为 macOS/Linux amd64、arm64，远端 runner 为 Linux amd64、arm64。交叉构建不等于运行验证。

`BUILD.json` 保存源码提交、工作树修改状态、Go 版本、目标平台和依赖声明清单；`loaf version --json` 提供对应的构建标识，缺少来源信息时如实显示未知，不伪装成干净构建。将 `archive` 设为压缩包的准确文件名、`version` 设为文件名中的软件版本，再在包含压缩包和校验文件的目录中执行：

```bash
# archive 是压缩包的准确文件名
grep -F "  $archive" "loaf_${version}_checksums.txt" | shasum -a 256 -c -
```

公开发布的压缩包附带 GitHub 构建来源证明，可用 `gh attestation verify "$archive" --repo elebirds/loaf` 检查，详见 [GitHub 验证说明](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations#verifying-artifact-attestations-with-the-github-cli)。本地构建包含元数据和下载校验和，不含 GitHub 签名的来源证明。

### 从源码构建

需要 Go 1.27.3、Git、OpenSSH 和 rsync。Go 版本固定在 `mise.toml`；使用 mise 时，请先检查该文件再信任项目配置。

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

构建会内置 Linux amd64、arm64 远端封装，并生成 `dist/loaf`、平台压缩包和 `loaf_<version>_checksums.txt`。默认构建标识由 `VERSION` 中的目标版本加开发提交和时间戳组成；显式设置 `VERSION` 环境变量可选择准确的软件版本。可用 `LOAF_BUILD_TARGETS` 选择其他构建目标，例如 `linux/amd64 linux/arm64 darwin/amd64 darwin/arm64`。
