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
| 6 | Creation wizard: Go, Node/TypeScript, Next.js, Python and FastAPI | Planned |
| 7 | Independent controller, process ownership, logs, readiness and Run & Preview | Planned |
| 8 | Ordered workspace execution, editors/URLs, delays and safe switching | Planned |
| 9 | Session capture, restore, branching and revision management | Planned |
| 10 | XDG Autostart with fixed, restore and hybrid modes | Planned |
| 11 | Save & Shutdown with verified snapshots and graceful managed stop | Planned |
| 12 | Reliability, accessibility, errors and UI polish | Planned |
| 13 | Linux binary/.deb, launcher, icon, license and release documentation | Planned |

## Current milestone

Project import/configuration, revision-checked rescans, ordered workspace association
and guarded registration removal are implemented. Automated import/review/reopen
checks cover conflicts, concurrent association/removal and source preservation.
Manual chooser/accessibility/X11 checks remain pending. Managed execution belongs
to Phase 7. The next implementation unit is Phase 6 project creation.

## Validation gates

- Go race tests/vet, frontend tests, production builds and native Wayland workflows.
- Manual GTK directory chooser, keyboard/screen-reader, scaling/focus and X11 checks.
- Controller close/reopen, restart reconciliation, cancellation and duplicate prevention.
- Session restore/branch independence and all three login startup modes.
- Shutdown state-machine/error tests using mocks; explicit approval before a real
  power-off test. Never force-close unrelated applications.
- Clean installation/uninstallation and preservation of project source directories.

No phase is complete until its checks pass and remaining limitations are recorded.
