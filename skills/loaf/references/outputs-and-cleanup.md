# Waiting, output retrieval, and explicit cleanup

For a foreground frozen group use `wait r_A r_B --timeout 30m --json`, or exact `wait --tag ablation`. Inspect all rows plus completed/pending/query-error and examined/unexamined coverage; cancellation does not stop tasks. Waiting does not schedule jobs.

## Retrieve outputs

```bash
loaf pull <run-id> --json
```

To browse before downloading, use `outputs <run-id> --depth 2 --limit 200 --json`. Check missing roots and truncation/depth/entry coverage. This is read-only metadata and does not deploy a helper; `RUNNER_REQUIRED` needs explicit `--prepare-runner` authority. Existing `pull` still prepares its helper automatically. Use `pull r_A r_B --into ./comparison --json` for separate run-ID directories and per-ID partial results. Concurrency is opt-in (1–4); interactive authentication stays serial. Do not combine batch `--into` with single exact `--to`.

The default uses output paths saved at submission and downloads to `loaf-results/<run-id>/` under the project. Changing the current target or project outputs does not redirect that task. Repeat `--path` to override the selection for one download; `--to` is the exact destination, relative to the current directory when not absolute.

`OUTPUT_MISSING` (exit 1) can still include useful files: inspect `data.downloaded_paths` and `data.missing_paths`. A failed transfer does not claim a completed `changed_files` count. Retry the same pull after a download error; do not restart the experiment to retrieve files. Downloads retain local extras, and canceling pull does not stop training.

Running tasks may be pulled, but the result is a live copy, not a consistent checkpoint snapshot. Choose completion or atomically published checkpoint files when needed. `--checksum` detects same-size, same-time content changes with extra disk reads; it is not normally required.

## Reclaim an authorized failed preparation

```bash
loaf abandon <run-id> --dry-run --json
loaf abandon <run-id> --yes --json
```

Use the ID retained after a failed transfer or copy. `abandon` fences that ID
before removing its payload; a local `preparing` or `uncertain` value alone does
not prove eligibility. The remote owner refuses any entered launch admission,
even before a Supervisor PID exists. Queued runs use `queue cancel` and
`queue clean`, not abandonment. Preview does not deploy a helper or change files.

Deletion needs explicit authority. Read each row's `no_start_at` and `cleaned_at`
separately. `ABANDON_UNCONFIRMED` (exit 3) retains confirmed no-start evidence;
retry only the same authorized ID to finish or confirm deletion. `state_error`
does not erase confirmed remote facts. `inspect ID --json` exposes saved
`abandonment` metadata without inventing a training terminal status. Shared
source cache and local downloads remain; an incomplete cache still needs an
explicit successful sync before new work can use it.

## Delete authorized terminal payloads

```bash
loaf clean <run-id> --dry-run --json
loaf clean <run-id> --yes --json
```

Cleanup requires explicit IDs and authority to remove their remote working files, logs, and outputs. Finishing an experiment does not itself authorize deletion. Use the read-only preview to choose scope when needed; `--yes` supplies the noninteractive confirmation once deletion is authorized. A preview does not reserve eligibility: actual deletion rechecks the run. Only confirmed submissions with a freshly verified terminal result are eligible; `clean` cannot reclaim unsubmitted preparation data (`submission: "preparing"`).

Local downloads, other tasks, source caches and shared environments remain. Small terminal/ID records are retained to prevent duplicate execution. `status` preserves the original result with cleanup metadata; `RUN_CLEANED` from `logs`, `pull` or `stop` is expected, not a reason to select an older run or repeat training. For a partial or unconfirmed cleanup, inspect per-ID results and retry only those same explicitly authorized IDs. Do not stop active tasks implicitly to make them eligible.

Choose data, dependencies, smoke tests and scientific acceptance for the actual experiment. Loaf does not assess metrics, automatically repair environments or schedule workloads. Neither a request to run an experiment nor permission to edit code authorizes paid acquisition or whole-instance shutdown.
