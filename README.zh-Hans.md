# Loaf

[English](README.md) | [简体中文](README.zh-Hans.md)

Loaf 围绕轻量 SSH 科研工作流开发。当前 CLI 提供版本信息、双语帮助和 JSON 输出，机器管理和远端执行尚未提供。

## 从源码构建

需要 Go 1.27.1 和 Git。信任 mise 项目环境前，请检查 mise.toml。

```bash
bash scripts/build.sh
./dist/loaf --help
./dist/loaf version --json
./dist/loaf --lang zh-Hans --help
```

构建在 dist/ 生成本地可执行文件和压缩包，目前尚无正式安装包。

## 语言与输出

通过 --lang、LOAF_LANG 或 ~/.config/loaf/config.toml 中的 language 选择英文或简体中文。默认 auto 使用系统语言。配置包含 format_version = 1。

JSON 结果包含 schema_version: 1、ok 和结构化数据或错误。字段和错误码不随语言变化。操作退出码：0 为成功，2 为参数或配置错误，1 为其他失败。

## 开发

使用 go run ./scripts/format.go 进行四空格 Go 格式化，使用 bash scripts/check.sh 运行格式、单元、静态、竞态和漏洞检查。漏洞数据库需要网络访问。

## 许可证

[MIT](LICENSE)
