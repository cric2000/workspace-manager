# Incremental implementation roadmap

Each milestone leaves the application usable. Do not start the next phase until
the current phase's automated checks pass and manual limitations are recorded.
The full product requirements are preserved in [product-brief.md](product-brief.md).

| Phase | Deliverable | Gate |
| --- | --- | --- |
| 1. Foundation | Wails + React/TypeScript, Tailwind, typed bridge, diagnostics | Go checks, frontend tests/build, native build and window smoke check |
| 2. Models and storage | Distinct models, JSON repositories, XDG, migrations, backups | CRUD, corruption, atomic-write and migration tests |
| 3. Workspaces | Create/edit/duplicate/delete/default profiles | Service and accessible UI interaction tests |
| 4. Applications | Desktop discovery, picker, launch, instance preferences | Spec fixtures and real desktop launch checklist |
| 5. Import | Read-only detection and reviewed approved startup configs | Polyglot fixtures, ambiguity and approval tests |
| 6. Bootstrap | Five-template registry and creation wizard | Isolated staging, no-overwrite, cancellation and dependency-failure tests |
| 7. Controller and run | Single controller, Unix IPC, supervised processes, readiness, output | Ownership, duplicates, cancellation, reconnect and GUI close/reopen tests |
| 8. Workspace execution | Ordered apps/projects/editors/URLs and safe switching | Multi-project launch, retry and hybrid identity tests |
| 9. Sessions | Capture, inspect, restore, branch, revisions and warnings | Immutable branch, update recovery and merge tests |
| 10. Login startup | XDG Autostart, fixed/restore/hybrid modes | Isolated autostart lifecycle tests; manual login test |
| 11. Save & Shutdown | Verified save, graceful stops, normal desktop shutdown request | Mock state-machine/error tests; separately approved manual shutdown |
| 12. Reliability | Accessibility, themes, errors, log limits and crash recovery | Frontend workflows and full Linux manual checklist |
| 13. Release | AMD64 binary, .deb, launcher/icon, license and installation docs | Clean-machine install/uninstall and acceptance scenarios A–F |

Controller architecture is decided now and implemented before any real server
execution in Phase 7. Phase 1 does not claim process persistence or session restore.

## Phase 1 units

1. Review architecture and inspect environment/dependency versions.
2. Generate official pinned Wails React/TypeScript template.
3. Add a read-only foundation service and generated frontend bindings.
4. Add Tailwind and an accessible foundation screen with real bridge status.
5. Add meaningful Go and Vitest checks, build, and record native launch evidence.

## Manual acceptance gates

- GNOME Wayland: window rendering, keyboard navigation, dialogs and launch behavior.
- X11: best-effort native window smoke check on a separate X11 session.
- Managed process: GUI close/reopen, controller crash/restart, identity reconciliation.
- Login: fixed, restore and hybrid modes without duplicate servers.
- Session: branch an older snapshot; original stays independently restorable.
- Shutdown: explicit user approval for a test that actually powers off the machine.
- Packaging: source directories survive metadata deletion and uninstall.

Record observed results, not assumed success. No real shutdown in automated tests.

## Phase 2 units

- [x] Initial workspace/project/session/settings records, stable IDs and validation.
- [x] JSON round-trip and independent session-branch value tests.
- [x] Side-effect-free XDG config/state/cache resolution and isolated tests.
- [x] Versioned JSON envelope, migration policy and strict decoding.
- [x] Verified atomic replacement, bounded reads and private-entry checks.
- [x] Private directory provisioning and repository transaction locking.
- [x] Workspace/project/settings repositories and immutable session revisions.
- [x] Corruption/last-good recovery tests and a complete storage milestone review.

Unit 1 is explained in [domain-foundation.md](domain-foundation.md); unit 2 in
[storage-format.md](storage-format.md); unit 3 in [repositories.md](repositories.md).
Phase 2's initial-model storage milestone is complete, with per-repository
transactions, stale-edit detection and explicit history recovery. Record shapes
will gain feature-specific fields and migrations incrementally.

## Phase 3 units

- [x] Profile service with server-owned IDs/timestamps and validated drafts.
- [x] Create/edit/duplicate/delete using repository revisions and recovery history.
- [x] Default selection/clearing, preserved settings and guarded default deletion.
- [x] Shared operation lock for cooperating GUI instances.
- [x] Persistent workspace page, search, modal editor and delete confirmation.
- [x] Safe interrupted-response handling and preserved conflicting drafts.
- [x] Go service/race tests and frontend workflow tests.
- [x] Native Wayland CRUD, dialogs, minimum-width layout and close/reopen persistence.

Native validation and remaining manual checks are recorded in [validation.md](validation.md).
The [workspace walkthrough](workspaces.md) explains this unit. Next is Phase 4:
desktop-entry discovery, an application picker and reviewed launch behavior.

## Phase 4 units

- [x] Read-only GIO desktop catalogue and stable desktop-ID resolution.
- [x] Isolated precedence, visibility, locale and availability fixtures.
- [x] Read-only installed-application check on the target host.
- [x] Typed desktop service, searchable catalogue selection and icon rendering.
- [x] Native search, details, local/themed icons and live add/remove refresh.
- [x] Workspace application configuration with migration and duplicate preferences.
- [x] Reviewed desktop launching, result/retry UI and real GIO helper-launch validation.
- [x] Custom executable configuration and conservative instance checks.

Unit 1 is explained in [desktop-discovery.md](desktop-discovery.md). Discovery
does not launch programs or establish running/readiness state. Unit 2's service and
UI are explained in [application-catalogue.md](application-catalogue.md). Phase 4 is
ongoing. [Unit 3](workspace-applications.md) now saves ordered application choices
in profiles. [Unit 4](application-launching.md) adds individual reviewed GIO launches
with same-token result checks. Installed graphical/D-Bus/terminal behavior remains
a manual gate. [Unit 5](custom-applications.md) now saves workspace-scoped custom
tools, reviews saved revisions and checks supported/configured session D-Bus
identities. Unknown SkipIfRunning results require an explicit AlwaysLaunch review.
Phase 4 implementation units are complete; the manual graphical/terminal/focus
gates remain recorded in [validation.md](validation.md). Next is Phase 5: read-only
project detection and reviewed startup configuration, with no project code execution.

## Phase 5 units

- [x] Read-only bounded metadata/source inspection and safe project-service errors.
- [x] Root JavaScript/TypeScript managers/scripts, Go mains, Python/TOML, Cargo,
  Compose and simple Make target evidence with ambiguity/limit findings.
- [x] Projects inspector UI with candidate argv, required tools, cancellation and rescan.
- [x] Polyglot/unsafe-file fixtures, frontend workflows and native inspection validation.
- [x] Native directory chooser and persistent canonical project registration.
- [x] Reviewed startup command configuration and project/session schema evolution.
- [x] Environment references, readiness, preview, editor, stop and delay settings.
- [ ] Rescan comparison without silently overwriting approved configurations.
- [ ] Workspace project association and full import/approval workflow checks.

The [inspection walkthrough](project-inspection.md) explains unit 1. Inspection
creates no project registration, saves no command and runs no tool/project code.
Source-level Python entrypoint inference, richer monorepo handling and full Compose/
Make interpretation are not claimed. [Unit 2](project-registration.md) adds the
native chooser, reviewed registration and editable ordered core startup commands.
Saving starts no project process. Project/session schema 2 migrations infer no
approval. Phase 5 remains ongoing; OS chooser interaction remains a manual gate.
[Unit 3](project-settings.md) adds these auxiliary settings with explicit review,
configuration-bound approval, metadata-only environment reference checks and
project/session schema 3. All runtime behavior remains with the future controller.
