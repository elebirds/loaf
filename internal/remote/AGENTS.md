# Remote execution contracts

Use these rules when changing remote execution, its run/client callers, transport
or owning state. Command details live in the public guide and nearby tests.

## Identity and ownership

- Save the Run ID and frozen location as `preparing` before sync; save `uncertain`
  before the single launch, then `submitted` after acceptance. Preparation failures
  keep their identity. Never resume or launch work from an observation.
- Receive holds the project lock through source copying into an independent work
  directory. Do not replace native rsync with a relay or writable hard links.
- The Supervisor owns execution and its process group. Verify boot ID, process
  start identity, ownership and lifetime locks before signaling; `kill -0` is not
  sufficient. Cancellation of a client or log stream does not stop accepted work.
- Preserve accepted submission, observed status, query failure and local-save
  failure independently. Confirmed remote facts survive local persistence errors.
  A failed output consumer must not acknowledge its chunk.
- Queue admission/dispatch and Supervisor ownership are separate. Queue callers'
  prerequisites must not change the existing direct `Submit` contract.

## Deletion and state

- `clean` requires confirmed submission and a fresh terminal outcome. `abandon`
  handles unsubmitted preparation only: copying/prepared may qualify; actual
  `phase=starting`, process identities, submitted and Queue-owned runs do not.
  A copying record's `status=starting` is not launch admission.
- Abandon locks Run claim → project → payload → control → launch → owner; busy
  means refusal, not cancellation of another owner. Atomically retain the ID claim,
  no-start receipt and single event before deleting the selected payload outside
  SQL. Receive/copy/start/launch reject late requests with `RUN_ABANDONED`.
- Missing/empty roots need a durable fence, not a success inferred from absence.
  Reject nonempty unowned roots. Revalidate directory identity before deletion;
  never reclaim shared cache, downloads or adjacent directories.
- Keep no-start, deletion completion and training status separate. Lost confirmation
  or partial deletion returns exit 3; explicit same-ID retries recover the operation.
- Formal `1.x` local state is `stable-v1/state.db`; development and prereleases
  use `state-v4/state.db`. Local/project schemas are 4 and the machine queue schema
  is 2, with a separate formal/development series marker. Reject unsupported series
  and unknown formats before writes; do not import `v0.x`, development or RC work.
- Compatibility begins at formal `v1.0.0`: newer formal `1.x` clients manage older
  formal `1.x` work through supported formats. Keep origin software versions,
  Run/Queue identity, saved bindings, cursors and receipts unchanged. Exact packaged
  helper verification is separate from compatible control peers.
- Project remote state remains `.loaf-control-v2/state.sqlite3`. Keep that namespace
  fixed so another handler cannot bypass no-start through another database. The
  machine/user queue likewise retains its single `queue-v1` ledger; refuse a
  development ledger instead of creating parallel capacity accounting. Do not
  hot-replace active Supervisors or restart an old compatible Coordinator merely
  because its controlling CLI was updated.
- Use private SQLite files, WAL/FULL and short explicit transactions. Transactions
  never span network calls, file copying or runtime; OS locks own those lifetimes.

Transport uses existing SSH trust/authentication and preserves literal argv. Unix
clients use command-owned private masters; native Windows uses paired MSYS2 tools
and independent connections. See the installation/development manuals for platform
details. Test changed process/file/transport behavior in the existing isolated SSH
fixture; never use research jobs or user-owned connections as test resources.
