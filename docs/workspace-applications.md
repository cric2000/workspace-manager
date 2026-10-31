# Phase 4, unit 3: applications in workspace profiles

Open Workspaces, choose **Apps**, find an installed application, and add it to the
ordered list. Move entries up/down, choose a duplicate-launch preference, and save.
Cancel discards the draft. This unit saves configuration; it does not launch any
application, enable login startup or grant command-execution approval.

This walkthrough records the desktop-only unit. [Unit 5](custom-applications.md)
extends the same editor with custom executable configuration and workspace schema 3.

## Domain and persistence

`internal/domain/application.go` introduces desktop IDs, duplicate policies and
`WorkspaceApplication`. A profile stores at most 64 distinct desktop IDs. Position
in its application slice defines launch order, so there is no second order field
to keep synchronized. Policies are `skip_if_running` and `always_launch`.

The default means skipping a **confirmed** matching instance. A future launcher
must report uncertainty instead of treating an executable/process name as proof of
identity. These are preferences awaiting launch integration, not a claim that
running-instance checks already work. Custom executables and multiple configured
browser profiles will need explicit identity/configuration in their own units.

No localized names, paths, executable arguments or environment contents are copied
into this record. Names/icons come from discovery. A missing saved ID remains
visible with its ID and an unavailable label, allowing later reinstallation or
explicit removal without losing unrelated configuration.

`internal/storage/workspace_codec.go` selects **workspace schema 2**. Other record
kinds remain at schema 1. The schema-1 migration decodes the exact prior shape,
checks it, and supplies an empty application list. It refuses unknown old fields
rather than dropping them or inventing approval for new fields.

Reads migrate in memory without rewriting a profile. On the next explicit mutation,
the repository fingerprints and archives the exact old bytes, then performs its
normal verified atomic replacement with schema 2. Existing IDs, creation timestamps,
project references and revision conflict checks remain intact. Unknown newer
schemas still fail closed. Older binaries will reject schema-2 files rather than
silently editing away the application field.

## Service operation

`SetApplications(ctx, target, choices)` changes only application settings. It checks
the displayed revision before applying the draft. The existing feature/repository
locks serialize it with other profile operations. The service clones slices when
accepting a draft and producing views, preventing independent profiles/drafts from
sharing mutable application lists.

Every new ID must resolve to a visible registered desktop application. Existing IDs
may be retained even after uninstall or hiding, allowing offline configuration edits.
That distinction prevents invented paths/IDs from entering via the picker while
avoiding silent deletion of saved references. Discovery remains point-in-time;
launching must re-resolve current metadata and perform its own review/checks.

Normal name/description edits preserve applications. Duplication clones both the
ordered application configuration and project references into a new profile ID.
Removing an application reference does not uninstall it, stop it, delete its desktop
entry or touch any project directory.

## UI responsibilities

`frontend/src/components/WorkspaceApplications.tsx` owns a local ordered draft.
The native modal shows saved choices, availability, policy selects and move/remove
buttons beside a searchable installed-app list. Results expand in groups of 24;
selection is bounded at 64. The shared `ApplicationIcon` component preserves the
existing ID-only PNG-preview boundary and per-refresh icon cache.

Discovery failure leaves saved selections intact and permits editing/removing
existing references. Newly added applications which disappear before saving return
an actionable pre-commit error; the user can refresh or remove them and retry.
Catalogue refresh never replaces the local draft.

`Workspaces.tsx` sends the original revision and handles the same committed/unknown
outcome rules as other edits. A conflict retains the draft but blocks another save;
reloading does not silently rebase it. Close and reopen the editor to review the
latest saved configuration. Pending writes disable cancellation and other mutations.
Save is an explicit button; typing Enter in search does not submit the whole draft.

## Checks and commands

From the repository root:

```bash
export PATH="$PWD/.tools/bin:$PATH"
wails3 task test
wails3 build
./bin/workspace-manager
```

Focused backend and frontend checks:

```bash
go test -race ./internal/domain ./internal/storage ./internal/workspaces
go test -race ./...
go vet ./...
npm --prefix frontend test
npm --prefix frontend run typecheck
```

Build embedded frontend assets before root Go checks; do not run those steps in
parallel. As in unit 2, the system image-decoder tests require their normal helper
IPC and use isolated temporary caches.

New tests cover domain policies/limits, real schema-1 migration, exact history bytes,
unsupported fields/newer schemas, revision conflicts, ordered application persistence,
independent duplication, name-edit preservation, retained unavailable references,
invalid/unavailable additions, clearing one profile, cancellation, discovery retry,
draft conflicts and UI correction after an application disappears.

The existing opt-in native harness now saves two fixture applications, reorders them,
changes a policy, duplicates a profile, and checks on-disk JSON plus reopen behavior:

```bash
go build -tags production,mcp -buildvcs=false -o bin/workspace-manager-smoke .
python3 scripts/native-smoke.py
```

It also checks retention after removal of a test-owned registration, independent
editing of the copied profile, and modal layout at a narrow native CSS viewport.
The harness requests a physical resize, but may use native WebView zoom when
compositor/configure timing prevents a reliable programmatic resize. It reports
that fallback explicitly and checks a stable viewport before measuring layout;
physical window resizing remains a manual check. All test data uses
temporary XDG directories. Unit 3 requested no application launch; the shared harness
now also covers [unit 4](application-launching.md) by launching a test-owned helper.
No installed user application, managed project command or shutdown is requested.
Pixel review, OS keyboard behavior, screen readers, scaling and X11 remain manual.
