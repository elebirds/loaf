# 配置项目与个人默认值

[首页](../../README.zh-Hans.md) · [命令参考](commands.md) · [English](../en/configuration.md)

任务列表命令为 `status`，原配置节仍保留为 `[ps]`。

## 项目配置

项目按最近的 `loaf.toml`、Git 工作树根目录、当前目录的顺序识别，无须 `init`。可选的项目配置可以指定机器、远端目录和同步策略：

```toml
target = "autodl"
remote_dir = "~/projects/my-research"

[sync]
mode = "mirror"            # 改为 update 可保留远端已删除或被排除的旧文件
respect_gitignore = true
exclude = ["data/", "checkpoints/"]

[outputs]
paths = ["outputs", "checkpoints"]
```

目标选择顺序为 `--target`、当前项目的 `loaf machine use` 设置、TOML 中的 `target`。个人选择不会改写项目 TOML。未设置 `remote_dir` 时，各项目使用稳定 ID，在目标的远端基目录下拥有独立路径。可以通过 `loaf machine add ... --remote-dir '~/projects'` 设置基目录。

用户配置位于 `~/.config/loaf/config.toml`。项目标识、个人选择、任务位置、标签、观察、清理摘要及云操作收据统一保存在 `~/.local/state/loaf/state-v2/state.db`。`XDG_CONFIG_HOME`、`XDG_STATE_HOME` 分别覆盖配置和状态根目录。凭据仍保存在选定的 keyring 或独立私有 `credentials/` 目录。

v0.6 状态边界及匹配旧 CLI 的用法见[升级说明](upgrading.md)。

每个新远端项目在 `<远端项目>/.loaf-control-v2/state.sqlite3` 保存 schema 2 控制库；源码、逐任务工作副本、日志和产物仍是普通文件。两端数据库使用私有权限、WAL、FULL 同步和有界锁等待，未知 schema 明确失败。软件、CLI schema、wire record 和数据库 schema 分别管理版本。

在用户配置中增加 `[sync]` 可设置个人同步默认值，保留已有 `format_version` 和机器配置：

```toml
[sync]
mode = "mirror"
respect_gitignore = true
exclude = ["scratch/"]
```

个人默认值也支持 `[run] wait = "10s"` 和 `[logs] tail = 100`。按显式参数、项目偏好、用户偏好、内置默认（`wait = "0s"`、`tail = 100`）的顺序生效，零值可以显式覆盖。可执行命令、setup 和环境变量仅属于项目，不从全局配置继承。

项目与用户设置还支持 `[ps] limit = 20`、`[exec] cwd = ""`。列表数量接受 1–1000，显式零值是错误，不会回退默认。exec 目录的空字符串可显式恢复远端主目录，即使较低优先级配置选择了其他目录。两项均按命令行、项目、用户、内置默认的顺序生效。

`run.wait` 只作用于提交。`status <任务ID>` 仅在显式提供 `--wait` 时等待，列表查询不会等待任务结束。

同步策略按显式命令行参数、项目配置、全局默认、内置默认的顺序生效；内置默认是 `mirror` 和 `respect_gitignore = true`。标量逐项继承，排除项跨层追加，不替换内置保护规则。`respect_gitignore = false` 关闭 Git 跟踪及忽略选择，不关闭 Loaf 自身排除规则。`update` 保留远端已在本地删除或新被排除的旧文件，人类输出会提示这一点。JSON 包含生效的 `policy` 及选择、排除结果。

```bash
loaf sync --dry-run --mode update
loaf sync --respect-gitignore=false --exclude 'scratch/' --exclude '*.tmp'
loaf sync --json
```
