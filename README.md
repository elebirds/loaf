# Loaf

[English](README.md) | [简体中文](README.zh-Hans.md)

Loaf is being built around a small SSH research workflow. The current CLI provides version information, bilingual help, and JSON output; machine management and remote execution are not available yet.

## Build from source

Requires Go 1.27.1 and Git. Review mise.toml before trusting its pinned environment.

```bash
bash scripts/build.sh
./dist/loaf --help
./dist/loaf version --json
./dist/loaf --lang zh-Hans --help
```

The build produces the local executable and an archive in dist/. There is no installable release yet.

## Language and output

Choose English or Simplified Chinese with --lang, LOAF_LANG, or language in ~/.config/loaf/config.toml. The default auto uses the system locale. Configuration includes format_version = 1.

JSON results use schema_version: 1, ok, and structured data or errors. Keys and error codes do not change with the interface language. Operation exits are 0 for success, 2 for arguments/configuration, and 1 for other failures.

## Development

Use go run ./scripts/format.go for four-space Go formatting and bash scripts/check.sh for format, unit, static, race, and vulnerability checks. The vulnerability database requires network access.

## License

[MIT](LICENSE)
