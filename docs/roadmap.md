# Roadmap

The project is built incrementally. Completed implementation still needs the manual
acceptance checks listed below; this is not a complete V1 release.

| Phase | Scope | Status |
| --- | --- | --- |
| 1 | Wails/React foundation, diagnostics and themes | Implemented |
| 2 | Domain models, JSON repositories, migrations and recovery | Implemented |
| 3 | Workspace CRUD and default selection | Implemented |
| 4 | Application discovery, configuration and reviewed launch | Implemented |
| 5 | Existing-project import, detection and reviewed configuration | Implemented; manual gates pending |
| 6 | Creation wizard: Go, Node/TypeScript, Next.js, Python and FastAPI | Implemented; manual acceptance gates pending |
| 7 | Independent controller, process ownership, logs, readiness and Run & Preview | Planned |
| 8 | Ordered workspace execution, editors/URLs, delays and safe switching | Planned |
| 9 | Session capture, restore, branching and revision management | Planned |
| 10 | XDG Autostart with fixed, restore and hybrid modes | Planned |
| 11 | Save & Shutdown with verified snapshots and graceful managed stop | Planned |
| 12 | Reliability, accessibility, errors and UI polish | Planned |
| 13 | Linux binary/.deb, launcher, icon, license and release documentation | Planned |

## Current milestone

Project import/configuration, rescans, workspace association and guarded removal
are implemented. Phase 6 now provides a template registry and creation wizards for
Go, Node.js + TypeScript, Python, FastAPI and Next.js, with exact preview, staged publication,
cancellation and unapproved startup configuration. Creation does not execute
project code or install dependencies. Next.js preparation explicitly downloads and
runs pinned create-next-app in a private directory before a separate file review.

FastAPI saves a project-local venv interpreter, health check and docs preview;
its environment and dependency steps require separate approval after creation.

Next.js supports TypeScript/JavaScript, Tailwind, App/Pages Router, src directory
and npm/pnpm. Generated output is bounded and published without rerunning the tool.

Post-creation setup supports empty Git initialization, new Python environments,
npm/pnpm installs without lifecycle scripts, and FastAPI pip installation in its
owned environment. Setup keeps partial files and never grants startup approval.

Tool versions can be checked explicitly from creation and setup reviews; checks
report declared minimums without approving setup.

Setup attempts and bounded output persist across restart, with a read-only history
view and explicit handling of missing final outcomes. Setup still requires the
current creation session; history never grants retry or execution approval.

Creation and official scaffolding attempts also persist, including publication
failures and bounded tool output. Creation history is available without a project
registration and never resumes or retries an operation.

Restart recovery now checks per-attempt owner leases, destination metadata and
registrations. Missing registrations can be restored through a fresh review only
when the current tree matches the saved file hashes; startup remains unapproved.
Partial installs, temporary preparation output and older untracked attempts require
manual inspection rather than command replay or automatic cleanup.

Phase 6 implementation is complete; manual chooser/accessibility/X11 checks remain
pending. Next: Phase 7's independent controller, process ownership and Run & Preview.

## Validation gates

- Go race tests/vet, frontend tests, production builds and native Wayland workflows.
- Manual GTK directory chooser, keyboard/screen-reader, scaling/focus and X11 checks.
- Controller close/reopen, restart reconciliation, cancellation and duplicate prevention.
- Session restore/branch independence and all three login startup modes.
- Shutdown state-machine/error tests using mocks; explicit approval before a real
  power-off test. Never force-close unrelated applications.
- Clean installation/uninstallation and preservation of project source directories.

No phase is complete until its checks pass and remaining limitations are recorded.
