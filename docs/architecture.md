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
| `internal/platform/linux` | GIO, icons, desktop/custom launches and D-Bus instance checks |
| `frontend/src` | Pages, components and typed service wrappers |

Configuration lives under `$XDG_CONFIG_HOME/workspace-manager`, session state under
`$XDG_STATE_HOME/workspace-manager`, and caches under `$XDG_CACHE_HOME/workspace-manager`.
Unset/relative XDG variables use standard home-directory defaults. Private storage
uses directories with mode 0700, files with mode 0600, atomic replacement, revision
checks and recoverable history. Feature locks coordinate cooperating GUI processes.

Workspace/project/session schemas are version 3; settings remain version 1.
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
  Activity/outcome caches are transient; durable task logs and crash recovery remain
  pending. Tool presence is checked without executing or version-probing it.
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
  cleans project/staging files or stops processes; managed process recovery follows
  with the Phase 7 controller.
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

## Planned controller

Before managed project execution, introduce an independent controller so closing
Wails does not terminate development servers. Prefer modes of one Go executable,
with a single per-user controller, a private Unix socket and peer-UID verification.
The GUI reconnects through typed operations; no unauthenticated TCP control API.

The controller will own persistence and managed process groups, track process start
identity, reconcile after restart, and provide bounded logs, readiness, delays,
retry and cancellation. Stop only owned processes; force stop requires explicit
user action. Editor/stop commands remain explicit argv configurations.

Sessions restore supported applications and managed project configurations, not
unsaved buffers, browser tabs or terminal history. Branching preserves the parent.
Login supports fixed, restore and hybrid modes with duplicate prevention.

Save & Shutdown must confirm user intent, save and verify the snapshot, stop managed
services gracefully, then request normal desktop shutdown. Persistence failure
blocks that path; OS denial preserves the snapshot. Actual power-off tests require
explicit approval. GNOME Wayland restrictions and X11 behavior require manual checks.
