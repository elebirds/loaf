# Agent guide for Loaf

Loaf is a Go CLI for running local research projects on existing SSH machines. The core workflow is connect, sync, run in the background, inspect logs, stop, and retrieve outputs. Environment checks, English and Simplified Chinese, and structured JSON output belong to the first release.

Keep machine provisioning, GPU scheduling, queues, automatic retries, central services, Web UI, and MCP out of scope. Interactive SSH, ad hoc execution, and port forwarding follow the core workflow.

## Start here

1. Inspect `git status --short` and the existing diff. Preserve unrelated and untracked work; do not reset or clean the checkout.
2. Read the relevant README sections, the target code, its callers, and nearby tests. Read any more-specific `AGENTS.md` before editing that subtree.
3. Read `go.mod`, `mise.toml`, and existing scripts when present. Verify tools with `command -v` before diagnosing a missing runtime; use the project's declared versions through PATH and mise, not an assumed system installation. Review a mise configuration before trusting it.
4. Establish the behavior being changed and the smallest check that demonstrates it. Keep the implementation, affected tests, and user-facing examples in the same change.

## Available entry points

The current foundation provides root help, version, English/Simplified Chinese, and JSON output. Machine management and remote execution are not available yet.

- `go run ./scripts/format.go` formats Go with four-space indentation; use `-check` in checks. Do not run standalone gofmt.
- `go test ./...`, `go vet ./...`, and `go test -race ./...` validate changes.
- `bash scripts/build.sh` is the sole build entry point.
- `bash scripts/check.sh` combines checks, including called-dependency vulnerabilities; the public vulnerability database needs network access.

Keep CLI rendering local, errors structured, and both embedded catalogs aligned. Select language by --lang, LOAF_LANG, user language, then the system locale for auto. Keep JSON keys and error codes stable across languages.

Keep both READMEs consistent with available commands. docs/ is local-only and Git-ignored; do not force-add it or make builds depend on it. Governance, SECURITY, CHANGELOG, ADR, and dependency-update configuration remain deferred.

When authorized, use English type(module): description commit subjects and bodies without milestone codes. Do not push, tag, publish, or modify a real server without authorization.
