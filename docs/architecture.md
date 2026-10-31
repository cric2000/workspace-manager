# Architecture decision record

Status: desktop foundation, storage and Phase 3 profile editing implemented.
The GUI now calls the workspace feature service through generated Wails bindings.
Profile/settings writes use repositories and a shared feature operation lock.
Phase 4 adds the GIO discovery adapter and an Applications page with
typed service calls and bounded PNG icon previews. Profiles now save ordered desktop
IDs and duplicate policies through a separate revision-protected operation.
Individual applications now have short-lived reviewed GIO/custom argv launches,
saved custom tools and conservative session D-Bus instance checks. Ordered workspace
execution remains pending.
Phase 5 now exposes a read-only Projects inspector through a separate service.
It inspects bounded metadata/source and returns evidence and alternative startup
candidates. A separate reviewed registration flow now persists canonical projects
and ordered startup commands with configuration-bound approval. It checks saved
revisions and review-time file identities, and runs no project command. The native
directory chooser is injected at application composition. Reviewed configuration
now includes environment-file references, per-command readiness, stop and delay,
plus preview/editor preferences. Their runtime behavior, workspace association and
rescan comparison remain pending.
Reviewed: 2026-10-31.

## Product contract

Workspace Manager is a local Linux developer utility. Its lifecycle is:

Restore or create → initialize → launch → run → preview → save → shutdown → resume.

V1 targets Ubuntu 26.04.1 LTS, GNOME, Wayland, AMD64. X11 support is best effort.
It does not promise recovery of arbitrary unsaved application state, window
positions, browser databases, terminal scrollback, or external process ownership.

## Desktop foundation

Use the official Wails v3 React + TypeScript template and its build conventions.
Pin the CLI, Go module and frontend runtime together. Use GTK4 / WebKitGTK 6.0.
React owns presentation and interaction; Go owns validation and side effects.
Tailwind integrates through the Vite plugin. Add accessible shadcn/ui components
when a feature needs them, rather than installing a component catalogue up front.

## Controller lifetime

The intended release is one executable with GUI, controller, login and management
modes. The controller runs independently of a Wails window. It is the sole writer
of repositories and owner of managed commands. The GUI reconnects to it and can
close without stopping development servers.

Before managed project process execution is added:

- Acquire an exclusive per-user controller lock.
- Place a Unix socket in a private, owned directory under XDG_RUNTIME_DIR.
- Verify directory ownership/permissions and the peer UID; use no TCP control API.
- Expose typed, versioned operations, structured errors and sequenced events.
- Reconcile the runtime journal before starting or stopping persisted processes.
- Bound request sizes, log buffers and event queues; support reconnect snapshots.

Do not assume a Wails service context survives GUI shutdown. GUI lifecycle hooks
only disconnect the client. Controller exit, service stop and desktop shutdown are
different operations. Development builds may use separate GUI/controller binaries
from the same source when Wails tooling requires it.

Until the controller is implemented, the GUI's workspace service opens repositories
lazily and closes them through `ServiceShutdown`. It executes no project commands.
This is a deliberate incremental arrangement: move ownership behind the controller
before Phase 7. All cooperating profile/default operations take the dedicated
`config/workspace-manager/workspace-operations/` lock before repository locks.
Deletion of the current default is refused; users explicitly clear or replace it.
Each accepted mutation writes one primary record, avoiding a cross-file commit.
Manual writers bypassing this service do not participate in reference guarantees;
a dangling default from outside the service is shown explicitly in the UI.

## Domain boundaries

| Record | Purpose | Lifetime |
| --- | --- | --- |
| Workspace | Editable applications, project references, browser settings and launch order | Reusable configuration |
| Project | Canonical path, detection metadata, editor and approved command configurations | Registered codebase |
| Session | Versioned capture, workspace association, parent ID, restore configurations and warnings | Immutable snapshot by default |
| Runtime | Launch ID, task states, owned process identities, readiness and operation state | Transient/reconciled state |

Projects can appear in several workspaces without duplicating directories.
Snapshots retain the approved launch configurations at capture time. They refer to
project IDs and paths, but do not rely exclusively on mutable current project
settings. A missing project record therefore remains diagnosable. A project command
changed by snapshot restore must be reviewed before gaining execution approval.

Session branching creates a new stable ID and parent reference. Explicit updates
preserve the prior revision before replacing the current revision. Rename changes
are also recoverable. Deleting metadata never deletes source directories.

## Packages and interfaces

Add packages when their first feature is implemented; avoid empty directories.

| Boundary | Responsibility |
| --- | --- |
| Root entry point / internal/app | Mode selection, composition and small typed Wails facade |
| internal/domain | Validated persistent and runtime types, independent of Wails |
| internal/storage | XDG paths, versioned JSON, migration, atomic persistence |
| internal/workspaces, projects, sessions | Feature services and repository operations |
| internal/projects/detection | Read metadata without executing project code |
| internal/projects/bootstrap | Template registry, file plans, staging and initialization |
| internal/applications | Desktop metadata discovery and recognized instance identities |
| internal/startup | Ordered tasks, delays, retry, cancellation and deduplication |
| internal/processes | Owned process groups, output capture, exits and reconciliation |
| internal/readiness | Context-bound HTTP/TCP checks with timeouts |
| internal/shutdown | Explicit state machine and verified-save prerequisite |
| internal/platform/linux | GIO/desktop launching, proc identities, autostart and D-Bus |
| frontend/src/services | Typed facade calls and UI error translation |
| frontend/src/components, pages | Accessible presentation and user workflows |

Introduce interfaces at the consumer boundary for repository persistence,
application launch, process supervision, readiness and shutdown. Do not wrap every
function in an interface or create a general-purpose workflow language.

## Execution and ownership

Detection returns executable/argument/working-directory suggestions with reasons
and ambiguity warnings. It never executes them. Approval records the exact reviewed
configuration, including explicit shell use if any. Editing it invalidates approval.
Never concatenate a project name or path into a shell program.

The first detection unit lives in `internal/projects/detection`. Linux `openat2`
pins reads below the canonical selected directory without following metadata or
source symlinks. Nonblocking, regular-file-only reads and scan/parse budgets prevent
unbounded inspection. Independent JSON/TOML/Go/YAML/static-Make detectors never invoke
tools or import/evaluate code. Candidate programs are tool names, not approved
executables. PATH checks inspect presence only. The bridge limits concurrency and
propagates cancellation; React discards stale results. No project repository is
opened by this service. See [project-inspection.md](project-inspection.md).

The controller starts command groups, drains stdout/stderr into bounded logs, reaps
children and records PID, process start identity, boot ID, project, command identity
and launch ID. Running and ready are different states; a short-lived CLI can succeed
without a web endpoint. Readiness and process exit race safely against cancellation.

Deduplicate by approved command/instance identity under a controller lock, not by
executable name. After a crash, only identities that can be verified remain eligible
for stop. PID reuse or uncertain group ownership must never result in a signal.
Use pidfds where available and validate the process group lifecycle before group
signalling. Graceful stop has a deadline and reports survivors; force stop is an
explicit action. Browsers, editors and unrelated terminals are never killed by name.

## Desktop integration and restore limits

Discover desktop entries in XDG user/system application directories with precedence
and Hidden/NoDisplay semantics. Prefer GIO's desktop launch facilities so field codes,
D-Bus activation and terminal requirements use established platform behavior. Do not
interpret an Exec value as a shell command. Custom commands remain explicit argv.

The first discovery adapter uses GIO's registered application list and menu
visibility checks. ID lookup searches that same list, including non-menu entries,
instead of using `g_desktop_app_info_new(id)`: the direct constructor can fall back
to a lower-priority system entry behind a minimal user Hidden tombstone. Keep this
guard when implementing launch resolution. Metadata and serialized icon references
are copied into toolkit-independent values. There is no GTK initialization,
arbitrary file-path lookup or launch operation in the discovery unit.

The catalogue UI uses `internal/applications.Service`. Its icon method accepts an
installed ID and re-resolves the visible registration, rather than accepting a
caller-supplied path or serialized icon. GTK icon-theme lookup is scheduled through
Wails `InvokeAsync`; the caller can cancel without waiting for a stopped main loop.
Bounded file reads and image decoding run off the GTK thread, with four concurrent
icon operations. Only a small PNG data URL reaches React. Local/themed icons use
the existing GTK/GdkPixbuf stack; unsupported or broken icons use a generic fallback.

`internal/applications.LaunchService` separately owns individual desktop-launch
reviews and outcomes. Tokens expire after five minutes, are bounded in memory and
can dispatch only once. Resolution rechecks effective metadata and desktop-file
bytes; changed entries require another review. GIO receives the exact checked
registration object and handles desktop semantics. A successful handoff establishes
neither readiness nor process ownership. Lost responses can be checked with the
same token while this GUI remains alive. See [application-launching.md](application-launching.md).

Workspace-scoped custom tools store an explicit executable, argv and working
directory. Review resolves their workspace revision and custom ID rather than
accepting caller-supplied commands. Revalidation covers saved configuration, file
identity and resolved symlinks before `exec.Command` dispatch. These are external
tool handoffs with detached sessions and discarded streams; they are not controller-
managed project commands. Closing the GUI does not deliberately terminate them.

Session D-Bus `GetNameOwner` checks establish a point-in-time configured service
identity. Desktop IDs qualify only with `DBusActivatable=true` and a valid well-known
name; custom tools may explicitly configure a documented name. Skip policy rechecks
at confirmation: owned means skip, absent means dispatch, unknown means no dispatch
until a fresh explicit AlwaysLaunch review. No process-name fallback, window capture
or signalling is implied. See [custom-applications.md](custom-applications.md).

Wayland offers no portable access to other clients' surfaces. Recognize supported
external applications through explicit adapters and reliable identifiers, show
uncertainty, and allow manual mapping. Do not present process scanning as complete
window capture. GNOME Shell extensions are outside the required V1 installation.

Browsers use explicit profiles/instance identities and native recovery settings.
Do not read browser session databases. Exact restored tabs depend on the browser.
Editor recovery is similarly best effort; no unsaved buffer capture is promised.

Restore validates before execution, reports missing applications/directories, and
continues independent tasks after failures. Hybrid mode merges stable application,
project-command and browser identities before scheduling. Never repeat a live server
just because it appears in both a snapshot and a workspace.

## Persistence

Use absolute XDG variables, ignoring relative values according to the specification:

- config: settings.json, profiles/, projects/
- state: sessions/, revision history, logs/, durable runtime journal
- cache: nonessential cached metadata
- runtime: ephemeral controller lock/socket, never a durable session store

Use private directories and files. Serialize writes through the controller. Write
temporary JSON beside the destination, validate it, sync it, rename atomically, then
sync the containing directory. Keep recoverable revisions and a last-good snapshot;
distinguish a committed write with a durability error from a pre-commit failure.
Version schemas, reject unsupported newer schemas and test migrations on fixtures.
Never persist environment-file contents, raw credentials or captured process memory.

Workspace schema 3 allows desktop references or custom executable configurations.
The workspace codec explicitly migrates schema 1 to an empty application list and
schema 2's exact desktop-only shape to schema 3, without writing during reads.
The next explicit mutation archives the exact previous document and writes schema 3.
Settings retain schema 1. Projects and sessions now use schema 3, with strict
schema-1/2 migrations. Legacy core command approval is preserved when new fields
are absent; no new approval or settings are inferred. Runtime fields are part of
startup approval; preview/editor preferences have their own approval. Session
branches deep-copy command argv and approval data. Desktop references contain only
ID and duplicate policy; custom records also contain explicit argv/path/identity
configuration. Workspace application configuration stores neither execution approval
nor live process state. Project command approval binds the saved literal configuration
and timestamp; it is not a claim about future source contents, readiness or runtime state.
Environment references remain project-relative paths. Existing references are
metadata-checked with rooted `openat2`/`O_PATH`; contents are not read. Endpoint
validation sends no requests. Editor/stop executables join review-time identity
checks. Actual environment loading/probing/signalling/launching belongs to Phase 7.
New selections must resolve through the visible catalogue; existing unavailable
references may be retained or removed explicitly. Normal edits and duplication
preserve application configuration independently of discovery availability.

## Bootstrap safety

All five templates share metadata, option validation, prerequisites, file plans,
external-command plans, dependency install plans and run/preview metadata. Internal
templates cover Go, TypeScript, Python and FastAPI. Next.js uses a verified, pinned
official scaffolder; supported flags are checked when the adapter is implemented.

Show exact files and argv before initialization. External scaffolding and dependency
installation require explicit approval. Validate names, module identifiers and path
boundaries. Resolve/reject symlink escapes and use directory-relative operations
where needed to prevent path substitution races. Stage on the destination filesystem
and publish with no-replace semantics. Never replace an existing target or recursively
clean arbitrary user paths. Cancellation preserves diagnostics and cleans only
proven operation-owned files. Downloads are identified in the review plan.

## Save & Shutdown

States: Idle → Confirming → Capturing → SnapshotSaved →
StoppingManagedProcesses → RequestingShutdown → CompletedRequest.
Failures and cancellation are explicit terminal outcomes. Shutdown Without Saving
requires its own explicit choice and skips capture, not process ownership checks.

Verify snapshot persistence before stopping services. If saving fails, remain running.
Keep the snapshot if service stopping or OS shutdown fails. Report stopped services
if the desktop later cancels shutdown; do not silently restart them. Ask GNOME's
session manager to handle normal interactive shutdown and application dialogs.
Inspect supported APIs on the target before implementing; capability-check logind
and never silently fall back to a less interactive shutdown path. No root or force
shutdown commands. Temporary inhibitors, if needed, must be bounded and released.
CompletedRequest means handed to the OS, not verified physical poweroff.

## Sources reviewed

- https://v3.wails.io/getting-started/installation/
- https://wails.io/status/
- https://wails.io/concepts/lifecycle/
- https://wails.io/quick-start/first-app/
- https://wayland.freedesktop.org/docs/book/Protocol.html
- https://specifications.freedesktop.org/basedir/latest/
- https://specifications.freedesktop.org/desktop-entry-spec/latest/exec-variables.html
- https://specifications.freedesktop.org/autostart-spec/latest/
- https://gnome.pages.gitlab.gnome.org/gnome-session/
- https://www.freedesktop.org/software/systemd/man/262/org.freedesktop.login1.html
- https://tailwindcss.com/docs/installation/using-vite

Exact APIs must be checked against downloaded dependency source before integration.
