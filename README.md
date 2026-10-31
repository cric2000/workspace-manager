# Workspace Manager

A local Linux desktop application for reusable developer workspaces and saved
sessions, built incrementally with Go, Wails v3, React and TypeScript.

**Current milestone: Phase 5 reviewed project configuration.** Create, rename, describe,
duplicate and delete profiles, select or clear the default, and reopen the app to
resume editing saved profiles. Revision checks protect stale edits. Deletion only
removes profile metadata; project directories are preserved. The app also provides
runtime diagnostics and light/dark appearance. Full workspace/project integration,
execution, session restore, autostart and shutdown remain later milestones.

The Applications page now browses installed desktop applications with search,
icon previews and launch-entry details. Refresh picks up desktop registrations
through GIO. Open **Apps** on a workspace card to save an ordered application list
and duplicate-launch preferences. Unavailable saved references are retained until
explicitly removed. Select an application and choose **Review launch** to inspect
its current registration before confirming a GIO launch. Interrupted responses can
be checked using the same request. Saving workspace choices opens no application.

Workspace Apps also accepts custom executable paths, literal argument lists and
working directories. Saved custom tools appear under Applications for reviewed
launching. Supported or explicitly configured D-Bus identities allow conservative
skip-if-running checks; unknown state blocks that policy until you explicitly
review with Always request a launch. The app never matches or kills processes by name.

The Projects page now inspects an existing directory and shows language/framework
evidence, scripts/entrypoints, package-manager conflicts, candidate argv and required
tool presence. Detection reads bounded metadata/source without invoking tools or
project code. Inspection does not register a project or approve/save/run a command.
Select a suggestion and choose **Configure project**, or add custom commands.
Review the resolved executable, literal arguments and working directory before
approving a save. Registrations persist and reopen for revision-protected editing.
Saving starts nothing. A native directory chooser is also available.
Project settings now include environment-file references, per-command readiness,
launch delay, graceful stop signal/action, a preview URL and an editor action.
Their exact values appear in review before saving. No environment contents are
read, endpoints contacted or editor/stop actions run by this configuration screen.

Read the [architecture](docs/architecture.md), [roadmap](docs/roadmap.md),
[validation record](docs/validation.md) and [original product brief](docs/product-brief.md).

## Requirements

The V1 target is Ubuntu 26.04.1 LTS, GNOME Wayland, Linux AMD64. X11 remains
best effort and requires its own manual acceptance checks.

- Go 1.26 or newer
- Node 24 LTS or newer and npm
- GCC, pkg-config, GTK4, WebKitGTK 6.0, GIO Unix and GdkPixbuf development packages
- Wails CLI **v3.0.0**, matching the Go dependency and frontend runtime

The official Ubuntu dependency command is:

```bash
sudo apt update
sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev
```

GIO's `gio-unix-2.0.pc` comes from `libglib2.0-dev`, also required by the GTK
development stack. GdkPixbuf is also supplied by that stack. The inspected
development host already has these libraries and their image decoder helpers.
No system packages were installed during Phase 1. Docker is not required.
See the [official Wails installation guide](https://v3.wails.io/getting-started/installation/).

## Setup, build and run

From the repository root, install the CLI locally and fetch the locked frontend
packages. These steps download development dependencies:

```bash
mkdir -p .tools/bin
GOBIN="$PWD/.tools/bin" go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
export PATH="$PWD/.tools/bin:$PATH"
npm --prefix frontend ci
bash scripts/check-environment.sh
wails3 doctor
wails3 build
./bin/workspace-manager
```

The build regenerates TypeScript bindings, builds the frontend, and embeds it in
the Linux binary. Runtime GTK/WebKit libraries remain system dependencies.
There is no frontend HTTP control server in the production build.

For native development with hot reload:

```bash
export PATH="$PWD/.tools/bin:$PATH"
wails3 dev
```

For a browser-only layout preview:

```bash
npm --prefix frontend run dev
```

The frontend server binds to `127.0.0.1:9245`. A browser preview cannot verify the
native Go bridge; use the native desktop build for bridge behavior. The default
Wails development port can be changed with `WAILS_VITE_PORT` if needed.

## Tests

Build the frontend assets before testing the root Go package, which embeds them:

```bash
export PATH="$PWD/.tools/bin:$PATH"
wails3 task test
```

Equivalent focused commands after a successful build:

```bash
go test -race ./...
go vet ./...
npm --prefix frontend test
npm --prefix frontend run typecheck
```

Go tests check runtime diagnostics, domain validation, JSON round trips, independent
session branches, settings, XDG paths, versioned decoding, migration handling,
atomic write failures, transactional CRUD, recovery and separate-process updates.
Workspace service tests cover persistent editing, project references, defaults,
concurrent deletion/selection and stale revisions. Vitest covers profile workflows,
dialogs, validation, conflict handling, interrupted responses, diagnostics and themes.
Catalogue tests cover search, selection, pagination, refresh, icons and unavailable
registrations. Image conversion tests use system decoder helpers and temporary
caches; run them where those helpers can create their normal IPC connections.
Launch tests cover review expiry, same-token replay, concurrent requests and
changed registrations. GIO and native fixtures launch only test-owned helpers.
Project-inspection fixtures cover polyglot metadata, ambiguity, build tags, malformed
documents, symlinks/FIFOs, scan limits and cancellation. Fake tools and script/Make
sentinels verify that inspection runs no tools or project code.
Registration tests cover canonical duplicates, competing services, stale revisions,
changed executables/directories, expiry, same-token replay, independent approval
snapshots and strict legacy project/session migrations. UI/native workflows cover
review, save/reopen, literal/empty argv and changed-executable rejection.
Project settings tests also cover loopback readiness/URL constraints, environment
symlinks/FIFOs and metadata-only checks, editor/stop identity changes, nested session
copies and schema-2 approval preservation without fabricated settings.
Native smoke observations and
outstanding manual checks are recorded in [validation.md](docs/validation.md).
No test invokes shutdown or starts a managed project command.

## First code units to study

1. [`main.go`](main.go) composes the Wails application, embeds assets and creates
   the native window. It contains no project execution or storage logic.
2. [`internal/app/info.go`](internal/app/info.go) is a small read-only service.
   Its allowlisted diagnostics do not copy arbitrary environment variables.
3. [`frontend/bindings`](frontend/bindings) contains generated TypeScript. Do not
   hand-edit it; run `wails3 generate bindings -ts -i` after changing Go services.
4. [`frontend/src/services/desktop.ts`](frontend/src/services/desktop.ts) keeps
   generated bridge imports outside UI components and propagates failures.
5. [`frontend/src/App.tsx`](frontend/src/App.tsx) composes navigation and themes.
   [`Workspaces.tsx`](frontend/src/pages/Workspaces.tsx) owns the profile workflows.
6. [`frontend/src/style.css`](frontend/src/style.css) provides theme tokens and
   desktop layouts; Tailwind is integrated through the official Vite plugin.

The controller will become an independent process before real server execution
is introduced. This GUI owns no development servers. Closing it exits the
GUI. Future controller lifetime and managed-service stop are separate operations.

## Scope and release status

This is an incremental implementation, not a complete V1 release. Packaging,
application icon, public module/repository identity, screenshots and license
selection are release-phase work. Generated non-Linux and Docker build targets
were removed from the starter to keep the V1 build scoped to Linux.

Read the [domain foundation walkthrough](docs/domain-foundation.md) and
[storage format walkthrough](docs/storage-format.md) for code, tradeoffs and focused
test commands. The [repository walkthrough](docs/repositories.md) explains locking,
revision tokens and explicit recovery. The [workspace walkthrough](docs/workspaces.md)
explains the service and editor. The GUI now creates private profile/settings files
under the XDG config directory. A separate operation lock coordinates profile and
default changes across GUI instances. Selecting a default does not enable autostart.
Theme changes currently last for the window's lifetime. The
[desktop discovery walkthrough](docs/desktop-discovery.md) explains Phase 4's first
backend unit. The [application catalogue walkthrough](docs/application-catalogue.md)
explains the desktop service, icon pipeline and UI. The
[workspace application walkthrough](docs/workspace-applications.md) covers saved
selections, order, duplicate policies and the original schema-1 → schema-2 migration.
Settings still use schema 1. Projects and sessions now use schema 3. The
[launch walkthrough](docs/application-launching.md) explains reviewed execution,
changed-entry protection, outcome checks and remaining desktop validation limits.
The [custom-tool walkthrough](docs/custom-applications.md) covers configuration,
schema 3 migration, revision-protected custom launch and conservative D-Bus checks.
The [project inspection walkthrough](docs/project-inspection.md) explains the new
detectors, filesystem boundaries, resource limits and unsupported/ambiguous cases.
The [registration walkthrough](docs/project-registration.md) explains reviewed saves,
command approval fingerprints, canonical registration, schema migration and tests.
The [project settings walkthrough](docs/project-settings.md) covers environment
references, readiness, preview/editor/stop/delay configuration, approval and migrations.
Next: explicit rescan comparison without overwriting approved configuration.
