# Architecture

## Current implementation

Go owns domain validation, persistence and Linux integration. React owns the UI
and calls Go services through generated Wails bindings. The pinned Wails CLI, Go
module and frontend runtime use version 3.0.0.

| Package | Responsibility |
| --- | --- |
| `internal/domain` | Workspace, project, session and settings models; command approval |
| `internal/storage` | XDG paths, strict JSON codecs, migrations, revisions and atomic writes |
| `internal/workspaces` | Workspace editing, default selection and ordered application/project references |
| `internal/applications` | Discovery facade, reviewed launch and duplicate handling |
| `internal/projects` | Detection, reviewed configuration/creation and guarded registration removal |
| `internal/projects/bootstrap` | Deterministic starter registry and staged publication without overwrites |
| `internal/platform/linux` | GIO, icons, desktop/custom launches, D-Bus instance checks and normal GNOME shutdown |
| `internal/lifecycle` | Local controller IPC, worker supervision, launch plans, snapshots, login and shutdown |
| `frontend/src` | Pages, components and typed service wrappers |

Configuration lives under `$XDG_CONFIG_HOME/workspace-manager`, session state under
`$XDG_STATE_HOME/workspace-manager`, and caches under `$XDG_CACHE_HOME/workspace-manager`.
Unset/relative XDG variables use standard home-directory defaults. Private storage
uses directories with mode 0700, files with mode 0600, atomic replacement, revision
checks and recoverable history. Feature locks coordinate cooperating GUI processes.

Workspace/session schemas are version 4; projects remain version 3 and settings version 1.
Migrations are strict, preserve valid legacy approvals, and never rewrite on read.
Sessions hold independent project snapshots; branching deep-copies nested settings.

## Execution boundaries

- Detection reads bounded metadata/source without invoking project tools or code.
- Configuration review shows the resolved executable, literal argv and working
  directory. Approval binds the reviewed configuration; edits require review again.
- Registered-project rescans verify the saved revision before and after inspection.
  Comparisons cover executable/argv/working-directory fields. Explicit replacement
  preserves command identity, name, order, runtime settings and project preferences;
  adding/replacing a candidate opens an unapproved draft for the existing save flow.
  Absent candidates never trigger removal. Scan results remain transient.
- Go, Node/TypeScript, Python, FastAPI and Next.js creation holds exact file bytes and a
  five-minute review token. Parent paths reject symlink components; publication uses a pinned descriptor and Linux
  rename-without-replacement. Cancellation before publication cleans owned staged
  files; after publication, source is preserved even if registration fails. Replayed
  tokens return cached outcomes; read-only outcome checks never retry creation.
  Nested files use pinned directory descriptors and cleanup preserves edited or
  unknown entries. Created projects retain unapproved startup commands.
  The Node starter uses Node 24.12+ native type stripping for development; npm
  setup is shown separately and never run during creation. Type checking/building
  use pinned development dependencies and an emitted JavaScript entry point.
  The Python starter runs/tests with the standard library, declares setuptools
  packaging metadata and shows optional venv setup separately. It saves system
  Python; creating a venv manually does not change the saved command.
  Review/outcome caches are transient; creation/setup histories and recovery checkpoints
  are durable. Tool presence is checked without executing or version-probing it.
  FastAPI adds required venv and dependency steps (separately reviewed after creation),
  a project-local interpreter, HTTP health readiness and a docs preview. Approval
  requires the interpreter to exist. Executable review preserves its invocation
  symlink, pinning the link, parent and resolved binary to retain venv semantics.
  Next.js uses pinned official create-next-app with explicit options and no Git or
  dependency installation. A separately approved, cancellable preparation runs in
  a private directory with isolated caches/configuration and a bounded process group.
  Preparation releases service locks; read-only outcome queries never retry it.
  Files are captured with bounded, no-symlink reads; binary assets use base64 across
  the bridge. A second approval publishes the held bytes without rerunning the tool.
- Post-creation setup has separate held reviews for Git, venv and dependencies.
  Only this app session's successful creation is eligible. Reviews pin the original
  directory and tools; dependency installs require the unchanged generated source.
  Existing setup output is refused. Git uses an empty template; npm/pnpm disable
  lifecycle scripts/workspaces/hooks; pip requires the flow's owned venv and may run
  explicitly approved build code. Cancellation stops only the owned process group,
  preserves partial files and caches its outcome without retry. Startup approval is
  unchanged. Output is limited to 64 KiB with proxy credentials redacted. Reviews
  remain transient; diagnostic setup history survives restart in XDG state.
- Setup logs use schema-1 records under `setup-logs/<project ID>/`, with private
  permissions and atomic synced writes. The attempt must be saved before spawning;
  final results are saved even after cancellation or service shutdown. History stores
  no review tokens, environment maps or executable instructions. A missing final
  result is shown as unknown, never treated as proof of a live process or retried.
  History is bounded to 32 records per project and 64 KiB output per attempt;
  unreadable/newer records are preserved and block further setup. A final-save
  failure reports the actual setup outcome and a separate persistence warning.
- Creation/preparation history uses schema-1 records under `creation-history/`,
  independent of registration. Attempts are synced before publication or tool
  execution; final saves preserve the actual operation outcome on storage failure.
  Scaffolder output is capped at 64 KiB, sanitized and stripped of the temporary
  review token. No source files, argv, environment maps or approval tokens are stored.
  History scans retain a chronological page of 20 records under a three-second
  read budget; corrupt/newer records fail the page and are preserved. Missing final
  outcomes stay unknown. Reading history cannot resume, retry or approve anything.
- Recovery checkpoints are separate schema-1 records under `bootstrap-recovery/`.
  They store unapproved publication configuration and bounded file hashes, with
  no review tokens or source contents. Per-attempt kernel lock leases identify
  active app owners across processes and release on process death. Recovery records
  observations without rewriting diagnostic outcomes or inferring successful exits.
  A fresh five-minute review can restore only a missing registration, after exact
  tree/hash and directory-identity checks repeated at commit under the project lock.
  Restored commands stay unapproved; lost responses have a read-only outcome query.
  Partial setup, temporary preparation output and older attempts without checkpoints
  require manual inspection. Recovery never replays tools, adopts partial installs,
  cleans project/staging files or stops processes. Managed workers have separate ownership.
- Tool version probes require an explicit action on a held creation/setup review.
  Fixed argv runs in an isolated temporary directory with pinned executables,
  three-second command/15-second total limits and 4 KiB output bounds. Only parsed
  stable versions and fixed messages reach the UI. Minimum checks are advisory;
  npm/pnpm/pip report detected versions without claiming feature compatibility.
  Go uses [local toolchains](https://go.dev/doc/toolchain); Corepack
  [network access is disabled](https://github.com/nodejs/corepack#environment-variables).
  Reviews and file publication continue to execute no tools.
- Environment references store project-relative paths, never file contents. Existing
  references are checked with rooted metadata-only operations; symlinks and special
  files are refused.
- Readiness configuration is limited to loopback HTTP/TCP endpoints. Saving settings
  does not probe endpoints, open editors or execute startup/stop actions.
- Desktop/custom application launching is implemented. Unknown running-instance
  identity blocks skip-if-running; process names are never used as identity.
- Project association checks workspace and project revisions. Existing missing references
  are retained until explicitly removed. A shared workspace-operation lock coordinates
  association, duplication and registration removal across GUI processes; removal
  refuses referenced registrations or unreadable workspace storage, archives metadata,
  and leaves source files and independent session snapshots intact.
- Metadata deletion must preserve project directories. Raw secrets and arbitrary
  application memory must not enter configuration or session snapshots.

## Controller and workers

One executable supports GUI, `--controller`, `--task-worker ID`, `--login`,
`--status` and `--stop-controller` modes. GUI services reconnect over a 0600 Unix
socket in a 0700 directory, with peer-UID verification in both directions. A
kernel lease prevents duplicate controllers. Pinned directory descriptors avoid
socket path-length limits and symlink substitution. There is no TCP control API.

Each project command has an independent worker and per-command lease. The worker
owns its child's process group, bounded output, readiness and graceful/explicit
force stop. GUI/controller exit does not kill workers. Runtime metadata records
boot ID, PID and process start time; pidfds address workers without PID reuse.
A non-reaping wait and a shared signal/reap lock prevent process-group reuse
between ownership checks and signalling. Metadata write failures retain the live
worker so it can still accept an explicit stop rather than abandoning a service.
A controller restart reads metadata instead of adopting arbitrary child PIDs.
Old-boot records become stopped. Unknown workers or surviving descendants require
inspection; no process-name matching or blind group kill is used.

Runtime log ticks use atomic replacement without accumulating revision backups.
User sessions retain the ordinary revisioned repository. Known environment values
and common credential patterns are redacted, but arbitrary application output is
not a secret store. Environment files use bounded, no-symlink reads and literal
KEY=VALUE syntax; values never enter configuration or snapshots. The worker
rechecks executable/directory identity after delays and stop-action identity before
execution. These checks are not a sandbox for approved, mutable project source.

Readiness accepts loopback HTTP/TCP only. HTTP ignores proxies, refuses redirects,
pins localhost to loopback and bounds each probe. Readiness failure leaves the
process visible and owned; it does not silently kill or relaunch it.

## Launch, sessions and login

The task engine reviews current desktop/custom metadata, literal command/editor
argv, URLs, stop policies, delays and approval. Commit repeats validation and
returns a cached outcome per token. Accepted tasks run outside the GUI request
context. Cancellation skips undispatched tasks and preserves already running
services. Switching can preserve services or gracefully stop previous exclusive
project services before dispatching new tasks. Running project command identities
are reused rather than duplicated, including identical commands re-registered with
new IDs. Hybrid plans deduplicate canonical project directories before task creation.

Session capture stores known launch metadata, managed project snapshots and
supported external session D-Bus identities. It reports Wayland capture gaps;
unsaved documents, arbitrary tabs, terminal history and raw environment values are
not captured. Branches have independent IDs and a stable parent. Rename/update,
revision restore and deletion are explicit; ancestry and selected restore targets
are protected. Capture is persisted and read back before reporting success.

Fixed login launches a workspace; restore uses the selected snapshot; hybrid merges
both by stable task identity. A persisted fingerprint authorizes only the reviewed
plan. Changed applications/configuration require another review. One dispatch
marker per desktop session prevents replay after controller restart. A failed or
interrupted login remains visible for manual review rather than automatic replay.
The owned [XDG Autostart entry](https://specifications.freedesktop.org/autostart/latest/)
quotes literal executable paths and refuses to overwrite unrelated entries.

## Shutdown and distribution

Shutdown progresses through confirming, capturing, snapshot_saved,
stopping_managed_processes, requesting_shutdown and completed_request, or failed/
cancelled. Capture/verification failure stops the workflow before any service
signal. Graceful-stop failure prevents OS handoff; force stop is a separate explicit
action. The [normal GNOME Shutdown API](https://gnome.pages.gitlab.gnome.org/gnome-session/re04.html)
requests the desktop shutdown dialog. A successful call is only an accepted request;
the desktop/user can still cancel. There are no force shutdown commands or inhibitor
bypasses. Automated tests use mocked OS handoffs.

The Debian package installs one production executable, desktop entry, scalable icon
and license/docs, with no post-install execution or cleanup of user data. Production
builds exclude Wails MCP and screenshot services. Native smoke builds enable them
only for isolated fixtures; WebKit snapshots capture app content without desktop access.
