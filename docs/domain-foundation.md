# Phase 2, unit 1: records and paths

This unit adds pure Go domain records and XDG path calculation. It does not yet
save files, capture the desktop, execute commands or expose new GUI actions.
No dependencies were added. The existing desktop application remains usable.

## Read the files in this order

1. [`id.go`](../internal/domain/id.go): opaque random IDs are separate from editable
   names. Validation rejects path separators and traversal strings before an ID
   can be used as a repository filename.
2. [`workspace.go`](../internal/domain/workspace.go): a workspace holds project IDs.
   Duplicate references are rejected; an empty profile is valid.
3. [`project.go`](../internal/domain/project.go): a project holds its canonical
   registration metadata. Paths must be absolute and lexically clean. Directory
   existence, symlink resolution and installed tools are checked by later services.
4. [`session.go`](../internal/domain/session.go): sessions copy project records.
   `Branch` creates a new ID, records the immediate parent and clones slices so
   edits cannot affect the source. It keeps the same physical project path.
5. [`settings.go`](../internal/domain/settings.go): defaults work before any profile
   exists. Enabling autostart requires the selections needed by its mode. Delay
   is bounded to 0–3600 seconds. IDs are checked structurally, not looked up.
6. [`paths.go`](../internal/storage/paths.go): calculate application config, state
   and cache paths using XDG overrides, with home-directory defaults. No directories
   are created and no permissions are changed.

## Structural validation versus preflight

Validation checks names, IDs, timestamps, enums, paths and duplicate references.
Errors identify the offending field, including nested indexes. Names support
Unicode and are limited by character count, not UTF-8 byte count.

A saved project can be structurally valid while its directory is temporarily
unavailable. Rejecting it on read would make recovery harder. Filesystem availability,
required executables, cross-record references and command approvals belong in
feature services and restore preflight, not these value types.

Dates must be nonzero and serializable by Go's JSON timestamp encoding. Updated
timestamps cannot precede creation. The branch operation normalizes capture time
to UTC and rejects a self-parent reference during validation. Cross-record ancestry
cycles will require repository-level checks.

## Why snapshots copy configuration

A workspace references the current project registration. A session preserves what
was captured. Editing a registry entry after saving must not rewrite history.

`Session.Branch` copies the existing snapshot; it does not capture newly opened
projects. The later capture service will collect current supported state before
creating a new session. Exported Go structs are mutable values; immutable saves
and recoverable explicit updates will be enforced by repositories.

When project/application configurations gain nested slices or maps, their cloning
must be added to `Branch` and its independence tests. Physical source directories
must never be cloned or deleted by session operations.

## XDG behavior

| Purpose | Override | Default application path |
| --- | --- | --- |
| Config | `XDG_CONFIG_HOME` | `$HOME/.config/workspace-manager` |
| State | `XDG_STATE_HOME` | `$HOME/.local/state/workspace-manager` |
| Cache | `XDG_CACHE_HOME` | `$HOME/.cache/workspace-manager` |

The [XDG specification](https://specifications.freedesktop.org/basedir/latest/)
requires absolute XDG paths. Relative overrides, including `~/...`, are ignored
and fall back to HOME. An absolute HOME is needed only for paths that use defaults;
valid explicit overrides work without it. Values are read on each call.

`SettingsFile`, `WorkspacesDir`, `ProjectsDir` and `SessionsDir` derive the agreed
file layout. These are lexical paths, not proof of safe filesystem access. The
repository implementation must still handle symlinks, permissions and concurrent
writes when opening them. Runtime socket/lock directory ownership and fallback
policy are deferred to the controller; no insecure `/tmp` fallback is introduced.

## Deliberately deferred

- Schema-versioned JSON envelopes, defaults/migrations and corruption recovery.
- Atomic persistence, private directory creation and repository concurrency.
- CRUD and reference lookup, session revisions and storage-level immutability.
- Application/browser configurations, project detection and approved commands.
- Runtime tasks/process identities, separate from all permanent profile records.
- Wiring settings into the GUI or writing user configuration on startup.

These are initial record shapes, not the complete V1 schema. Unit 2 now implements
the [versioned storage format and atomic JSON primitive](storage-format.md).
Unit 3 now provides [repositories, locking and recovery](repositories.md). Feature
services, capture/restore orchestration and GUI workflows remain later work.

## Verification

From the repository root:

```bash
go test -race ./internal/domain ./internal/storage
go test -race ./...
go vet ./...
```

The root-package test requires existing frontend build assets, as in Phase 1.
Tests exercise JSON round trips, invalid input, independent multi-generation
branching, autostart selections, XDG defaults/overrides and side-effect-free path
resolution. Temporary directories isolate path-resolution tests from real user
configuration. No native GUI changes or new manual desktop tests are required
for this unit.
