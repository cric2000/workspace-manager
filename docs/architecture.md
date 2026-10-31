# Architecture

## Current implementation

Go owns domain validation, persistence and Linux integration. React owns the UI
and calls Go services through generated Wails bindings. The pinned Wails CLI, Go
module and frontend runtime use version 3.0.0.

| Package | Responsibility |
| --- | --- |
| `internal/domain` | Workspace, project, session and settings models; command approval |
| `internal/storage` | XDG paths, strict JSON codecs, migrations, revisions and atomic writes |
| `internal/workspaces` | Workspace editing, default selection and application configuration |
| `internal/applications` | Discovery facade, reviewed launch and duplicate handling |
| `internal/projects` | Read-only detection and reviewed project registration/configuration |
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
- Environment references store project-relative paths, never file contents. Existing
  references are checked with rooted metadata-only operations; symlinks and special
  files are refused.
- Readiness configuration is limited to loopback HTTP/TCP endpoints. Saving settings
  does not probe endpoints, open editors or execute startup/stop actions.
- Desktop/custom application launching is implemented. Unknown running-instance
  identity blocks skip-if-running; process names are never used as identity.
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
