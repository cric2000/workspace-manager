# Phase 2, unit 2: versioned JSON and atomic replacement

This unit implements encoding, bounded file reads and atomic replacement. Higher
layers added in [unit 3](repositories.md) and [Phase 3](workspaces.md) now use these
primitives for persistent desktop profiles. Tests write only inside isolated
temporary directories.

## Files to study

- [`codec.go`](../internal/storage/codec.go): typed envelopes, domain validation,
  bounded JSON checking and explicit migrations.
- [`atomic_linux.go`](../internal/storage/atomic_linux.go): private-directory and
  file checks, descriptor-relative reads, verified temporary writes and commit
  error semantics.
- [`codec_test.go`](../internal/storage/codec_test.go): record round trips,
  malformed/ambiguous documents, unsupported versions and migration failures.
- [`atomic_linux_test.go`](../internal/storage/atomic_linux_test.go): real-file
  integration tests and narrow fault injection at the commit boundary.
- [`settings-v1.json`](../internal/storage/testdata/settings-v1.json): the first
  persistent-format fixture, independent of generated frontend bindings.

## Format

Every document has an explicit version, record kind and data payload:

```json
{
  "schemaVersion": 1,
  "kind": "settings",
  "data": {
    "startupMode": "fixed",
    "autostart": false,
    "initialDelaySeconds": 0,
    "appearance": "system"
  }
}
```

The other kinds are `workspace`, `project` and `session`. `Codec[T]` derives kind
from the Go domain type, preventing callers from assigning a settings kind to a
workspace payload. The type constraint excludes arbitrary maps and runtime state.
Domain validation runs before encoding and after decoding/migration.

Schema 1 is the initial format. There were no persisted application records in
unit 1, so no unversioned or fictional version-zero migration is provided.
Versions are interpreted for a record kind; repositories select that kind's
current codec version and explicit migration table. Persistent field changes must
be accompanied by an intentional schema decision and fixture updates.

## Decoding policy

- Maximum document size: 4 MiB; reads stop after the limit plus one byte.
- Maximum JSON nesting: 64 levels.
- Reject invalid UTF-8, malformed/truncated JSON and trailing JSON values.
- Reject duplicate keys at every depth, including escaped keys and Unicode casing
  aliases that Go's struct decoder would otherwise treat as the same field.
- Reject unknown envelope and current payload fields.
- Reject missing/null payloads, wrong record kinds and invalid domain values.
- Reject schema versions below 1 or newer than the configured codec.

The codec uses the stable `encoding/json` implementation, retaining its scalar,
timestamp and case-insensitive struct-field matching rules. It is not a general
JSON Schema validator. Errors do not echo parser-supplied field/value text.

Each migration transforms payload version N to N+1. A missing step or a failed
transformation returns an error and no partial domain value. The registry is copied
at codec construction; migrations receive their own input buffers. Callbacks must
be pure and safe for concurrent use. Migrated output is checked again, then decoded
and validated. Reads never save migrations back automatically.

The generic migration tests configure synthetic codecs to prove ordering, failure
handling and source preservation. Production now uses workspace schema 3 through
`NewWorkspaceCodec`, with explicit schema-1 and schema-2 migrations. Schema 2 added
desktop choices; schema 3 adds custom executable configuration. Projects and
sessions now use schema 3 through `NewProjectCodec` / `NewSessionCodec`; their
strict schema-1 migrations add no startup commands or approval. Schema-2 migrations
preserve valid legacy approval while adding no new runtime fields/preferences.
Settings retain
schema 1 (`InitialSchemaVersion`). See [workspace applications](workspace-applications.md),
[custom tools](custom-applications.md), [project registration](project-registration.md)
and [project settings](project-settings.md).

## Write protocol

`Codec.Write` first encodes/validates in memory, then:

1. Require a caller-owned directory with exact mode `0700`, opened as `*os.Root`.
2. Accept a single lowercase JSON filename, never a path or traversal component.
3. If the destination exists, require a user-owned regular file with mode `0600`.
4. Exclusively create a random temporary file in that same directory.
5. Set mode `0600` using the owned file descriptor, write all bytes and sync it.
6. Read back and compare the temporary file bytes, then close it.
7. Rename it over the destination atomically.
8. Sync the pinned directory descriptor.

Readers see a complete previous or replacement file. Same-directory rename gives
the commit boundary; syncing the file alone does not make the directory entry
durable. These choices follow the Linux [rename](https://man7.org/linux/man-pages/man2/rename.2.html)
and [fsync](https://man7.org/linux/man-pages/man2/fsync.2.html) contracts.

Before successful rename, a write/sync/verification/close/rename failure preserves
the old destination. Cleanup removes only the exclusive temporary name created by
that operation; it never deletes other files by prefix or recursively removes a
directory. Cleanup errors are returned alongside the original failure. A crash can
leave a temporary file, which must not be removed by indiscriminate startup cleanup.

After rename, a directory-sync failure returns `*storage.CommitError`. **The new
file is already visible.** Callers must reconcile the record and report uncertain
crash durability; they must not assume rollback or blindly retry. The implementation
does not claim that a successful write can defeat hardware or filesystem failures.

## Filesystem boundary

The caller opens the selected app directory once. Go's
[`os.Root`](https://go.dev/blog/osroot) pins it across renames and confines rooted
file operations. Existing directories or record files with unexpected permissions
are rejected; the primitive does not silently chmod user-owned directory trees.
Directory creation and syncing newly created directory ancestors remain repository
setup work for the next unit.

`os.Root` follows in-directory symlinks. To enforce the stricter record-read policy,
the Linux adapter uses `openat` on the checked directory descriptor with
`O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC`. The validated filename has no directory
components. Reads then verify that the opened descriptor is a private regular
file. This rejects internal/external symlinks and avoids hanging on a FIFO.

Tests also move the storage directory and replace its old pathname with a symlink;
subsequent writes remain in the originally opened directory.

## What atomicity does not provide

Concurrent replacements publish complete documents, but the last rename wins.
This is not a read-modify-write transaction, optimistic conflict check, immutable
session create, history backup or automatic corruption recovery.

`Codec.Write` is an explicit replacement primitive: it does not inspect the old
payload's version before replacement. Repositories must read/check under their
transaction lock, preserve revisions, and refuse to fall back to defaults and
overwrite a record after an unsupported-version or corruption error.

Unit 3 now adds [repository transactions, cross-process locking, private directory
provisioning and explicit revision recovery](repositories.md). Use those repositories
for application writes rather than calling the primitive directly. No controller,
real user configuration write or new GUI feature is introduced by these storage units.

## Verification

```bash
go test -race ./internal/storage
go test -race ./...
go vet ./...
```

Tests use actual temporary files and independent rooted directory handles. Fault
injection covers partial/short writes, file-sync failure, mismatched verification,
close failure, rename failure and post-rename directory-sync failure. Concurrent
readers/writers must never observe partial JSON. These tests do not simulate actual
power loss or claim cross-process transaction isolation.
