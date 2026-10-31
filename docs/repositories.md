# Phase 2, unit 3: transactions and recovery

The storage milestone now supports workspace/project/session repositories and a
singleton settings repository. [Phase 3](workspaces.md) connects this backend to
workspace services and the persistent desktop editor.

## Code responsibilities

Read these files after the [record models](domain-foundation.md) and
[JSON/atomic-write primitive](storage-format.md):

1. [`directory_linux.go`](../internal/storage/directory_linux.go) creates private
   directories, opens them by descriptor and acquires cancellable advisory locks.
2. [`records_linux.go`](../internal/storage/records_linux.go) implements the shared
   read/update/delete/history transaction and corruption-recovery rules.
3. [`repositories_linux.go`](../internal/storage/repositories_linux.go) provides
   typed entity and settings APIs, filename/ID checks and explicit session revisions.
4. [`repositories_linux_test.go`](../internal/storage/repositories_linux_test.go)
   exercises CRUD, concurrency, session semantics, migration saves and recovery on
   real temporary files.

The shared code avoids four unrelated persistence implementations. Feature packages
will depend on small interfaces around the operations they need; no empty service
packages or Wails bindings were added here.

## Directory layout and provisioning

Composition will use the paths resolved in unit 1:

| Repository | Open at | Primary filename |
| --- | --- | --- |
| Workspace | `Paths.WorkspacesDir()` | `<workspace ID>.json` |
| Project | `Paths.ProjectsDir()` | `<project ID>.json` |
| Session | `Paths.SessionsDir()` | `<session ID>.json` |
| Settings | `Paths.ConfigDir` | `settings.json` |

Each repository has a persistent `.repository.lock` and a private history tree:

```text
profiles/
  .repository.lock
  <workspace ID>.json
  history/
    <workspace ID>/
      <SHA-256 revision>.json
      damaged-<SHA-256 fingerprint>.json
```

`OpenPrivateDirectory` requires a clean absolute path and walks one component at
a time using descriptor-relative `openat`/`mkdirat`. It refuses symlinks, including
ancestor components. New directories use `0700`; existing directories are never
silently chmodded. The final app directory and history children must be owned by
the current user with mode `0700`. Existing shared XDG ancestors need not be private.

Parent descriptors are synced during provisioning, including retries after a
previous visible-but-not-durable mkdir. Linux `/proc/self/fd` converts the final
pinned descriptor into an `os.Root` without reopening the original pathname.
Ubuntu's normal procfs mount is therefore required. Configurations using symlinked
XDG paths must use their physical directory path for this V1 implementation.

Opening repositories provisions directories; it does not create default settings
or any primary record. The first transaction creates a private lock file. The GUI
does not open these repositories yet, so no real user configuration was written
during this unit.

## Transaction and revision contract

`Get` returns a `Snapshot[T]` with a domain value and `Revision`. The revision is
the SHA-256 fingerprint of the exact file bytes, including formatting and schema.
Keep it alongside the form values when editing a record.

- `Create` refuses any existing primary entry; there is no blind upsert.
- `Update` requires the revision read by the caller and a change callback.
- `Delete` also requires the expected revision.
- A stale revision returns `ErrConflict` before running the change callback.
- Invalid changes, identity changes and failed backups leave the primary unchanged.
- Workspace/project creation timestamps stay fixed and update timestamps cannot
  move backwards. Project source origin also stays fixed.
- Reading a record whose embedded ID differs from its filename is an error.

One repository transaction holds an exclusive
[`flock`](https://man7.org/linux/man-pages/man2/flock.2.html) across the current read,
revision check, validation, backup and primary replacement. Each operation opens a
fresh lock descriptor, coordinating goroutines, independent repository handles and
separate processes. The lock file is never unlinked; closing the descriptor releases
the lock. All application writers must use these repositories, since locks are
advisory and cannot protect against programs that bypass them.

Context cancellation interrupts lock waits and is checked before primary mutation.
The atomic commit itself is not cancelled midway. Callbacks execute once under the
lock, must be quick and must not call the same repository recursively. `Close`
waits for active operations, releases root handles and is idempotent.

These are per-repository transactions, not a database transaction across settings,
workspaces and projects. Reference lookup and cross-repository feature rules belong
to the application services. A failed `List` returns an error and no partial result.

## Session behavior

`Repository[domain.Session].Update` returns `ErrImmutable`. Use the separately named
`ReviseSession` operation for an explicitly confirmed rename/update; it preserves
the old bytes before replacing the primary. Session ID and parent ID stay fixed.
The frontend confirmation workflow will be added with session management.

Branching remains a new `Session.Branch` value followed by `Create`. New branch
parents must be present and readable; cyclic or excessive ancestry is rejected.
The ancestry walk is bounded to 1024 ancestors and observes cancellation. Copies
retain project IDs and source paths; they never copy source directories. Existing
children remain independently readable if a parent session is later deleted.

## History and explicit recovery

The first create archives a recovery copy before publishing the primary. Every
update and metadata deletion archives the current bytes before changing/removing
the primary. History files are content-addressed and verified before reuse.
Existing matching copies are synced again before relying on them.

`History` returns validated fingerprints in lexical order, **not chronological
order**. `GetRevision` loads the selected candidate and verifies its fingerprint,
kind, domain validity and embedded ID. There is no automatic "latest backup" guess.
A failed create can leave a prepared history candidate without a primary; such
candidates remain explicit recovery choices, not proof of a completed save.

`RestoreRevision` is an explicit rollback, undelete or corruption repair:

1. Validate the selected history candidate and its exact fingerprint.
2. Compare the primary against the caller's expected revision. For an absent
   primary, require an empty expected revision.
3. Refuse a primary from a newer/unsupported schema.
4. Archive a healthy primary, or quarantine the bounded damaged bytes.
5. Atomically publish the selected historical bytes.

A malformed readable primary returns `ReadError`, including its fingerprint for
this confirmation step. Recovery never overwrites unrelated changed data using a
stale damage fingerprint. Quarantine files are excluded from valid history results
and remain private; they are not logged or interpreted as session snapshots.

Permission, special-file, symlink, I/O and oversized-file errors are not silently
repaired. Reads never fall back to default settings after corruption. Backups are
retained after metadata deletion; no automatic retention or purge is implemented.
Project directories are never deletion targets.

## Commit errors at the repository boundary

Use `MutationError.Committed` to determine whether the **primary record** changed.
Its `Stage` identifies backup, primary write or metadata deletion. A nested
`CommitError` may refer only to a backup file, so inspecting that lower-level type
alone would give the wrong answer.

If a backup fails, primary mutation stops, even if the backup rename was visible.
If primary rename or metadata unlink succeeded but final sync failed, `Committed`
is true: report uncertainty and re-read before deciding the next action. There is
no multi-file rollback or claim that hardware failures are impossible.

## Verification and milestone boundary

From the repository root:

```bash
go test -race ./internal/storage
go test -race ./...
go vet ./...
```

The subprocess test runs two instances of the test executable against one temporary
settings repository. Together they perform 20 increments without losing updates.
Other tests cover stale edits/deletes, rejected callbacks, cancelled changes,
immutable sessions, branching, cycles, exact old-schema backups, undelete, damaged
primary quarantine, forged backup fingerprints, future-schema protection, private
directories and source-directory preservation.

Fault injection distinguishes a history-file commit error from a primary-file
commit error using actual writes. No real power loss, login startup, project launch
or shutdown was tested. The native application has no new behavior in this unit.

Phase 2's initial-model storage milestone is complete. Future features will extend
the domain schema with deliberate migrations. Phase 3 begins with workspace CRUD
services, duplication/default selection and the first persistent UI workflow.
