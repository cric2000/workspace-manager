# Phase 3: editing reusable profiles

The workspace service connects the tested repositories to the desktop. A draft
contains only a name and description. Go creates identity and timestamps; clients
cannot replace project references or immutable identity through this editor.
The [application editor](workspace-applications.md) now extends the profile through
a separate operation. Browser and other startup fields remain later work.

## Read the implementation

1. `internal/workspaces/service.go`: typed operations and safe response messages.
2. `internal/workspaces/service_test.go`: real temporary repositories and race cases.
3. `main.go`: registers the feature alongside read-only diagnostics.
4. `frontend/src/services/workspaces.ts`: generated transport calls outside React.
5. `frontend/src/pages/Workspaces.tsx`: loading, editing, confirmation and recovery.
6. `frontend/src/components/Modal.tsx`: native modal dialog and focus restoration.

No dependency was added for this milestone. The native HTML dialog provides the
browser top layer and inert background. This small editor does not require a new
component library. Wails' service shutdown contract and context injection were
checked against the pinned v3.0.0 source and its
[service documentation](https://v3.wails.io/features/bindings/services/).
The dialog follows the [HTML standard](https://html.spec.whatwg.org/multipage/interactive-elements.html#the-dialog-element).

## Operations and invariants

`List` opens private storage lazily. Only a missing settings file permits initial
defaults. Corrupt, unsupported, oversized and inaccessible data is never replaced
with defaults. A failed list returns no partial workspace collection. Successful
lists sort by case-insensitive name and then stable ID.

`Create` generates a random identity and UTC timestamps. `Edit` changes only draft
fields and the update time, preserving creation time and project IDs. A backwards
wall-clock adjustment cannot move the update time backwards. Names are trimmed
and validated by Go; duplicate display names are allowed because identity is an ID.

`Duplicate` checks the source revision before creating a new identity, copies the
description provided in the draft, and clones project references. It never opens,
copies or modifies a project directory. The copy does not become the default.

`Delete` checks its revision, preserves repository history and removes only the
selected profile. Deleting the default is refused until it is cleared or replaced.
That avoids a two-file mutation whose second write could fail after the first.

`SetDefault` checks both the workspace and settings revisions. Clearing is explicit
and cannot violate an existing autostart mode's requirement for a default. Other
settings remain intact. This milestone neither enables login startup nor launches
the selected profile. A default pointing at a missing profile, for example after a
manual file edit, is visible and can be cleared or replaced.

The service mutex serializes initialization, calls and shutdown. A dedicated
private-directory flock serializes complete feature operations across cooperating
processes, including state reads and selection/deletion guards. The lock ordering
is feature lock, then repository lock; never reverse it. `WithDirectoryLock` must
use a dedicated directory, since using a repository directory would deadlock.
Future services which modify these references must use the same coordinator or
the single controller. Direct manual/repository writes bypass this coordination.

## Errors and uncertain outcomes

Every response contains a full state on success or a code and safe message on
failure. It does not expose filesystem paths or raw transport errors. The UI sends
the revision it actually displayed, never silently retries a stale mutation and
keeps a conflicting draft available for reference. Reload does not rebase that
draft: close and reopen the editor to review the current record before saving.

`committed=true` on an error means the primary change took effect but durability or
the subsequent state refresh failed. The UI closes the submitted editor and blocks
new mutations until a reload. A rejected transport promise has an unknown outcome;
it also requires reconciliation before another submission. This prevents accidental
duplicate creation after a lost response. Confirmed pre-commit business errors
remain in the dialog with the draft intact.

The create/update buttons and cancel controls are disabled during a write. Delete
opens with focus on Cancel. After successful deletion, focus returns to New
workspace if the original trigger disappeared. Initial field focus is applied
after `showModal`; return focus is checked after React commits removed cards.
Theme changes are currently per-window; persistent appearance settings come later.

## Build and verify

From the repository root:

```bash
export PATH="$PWD/.tools/bin:$PATH"
wails3 task test
wails3 build
./bin/workspace-manager
```

Focused checks after the generated assets/bindings exist:

```bash
go test -race ./internal/workspaces ./internal/storage
go vet ./...
npm --prefix frontend test
npm --prefix frontend run typecheck
```

For the opt-in native smoke check in a graphical Wayland session:

```bash
go build -tags production,mcp -buildvcs=false -o bin/workspace-manager-smoke .
python3 scripts/native-smoke.py
```

The harness opens two consecutive GUI instances with the same temporary XDG
directories and checks profile/default persistence. It uses Wails' optional MCP
endpoint on loopback with a random token. The normal production build has no such
endpoint. Test files are deleted with their temporary root; no real project,
autostart entry or shutdown is involved. OS-level Escape/Tab input and pixel-level
rendering still require manual checks; MCP sends synthetic DOM events.
