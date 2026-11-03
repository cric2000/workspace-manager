# V1 roadmap

All thirteen phases have implementation in the repository. Automated checks do
not replace the desktop acceptance gates below; this is a release candidate until
those gates are verified.

| Phase | Scope | Implementation |
| --- | --- | --- |
| 1 | Wails/React foundation, diagnostics, themes | Implemented |
| 2 | Models, JSON storage, migrations, recovery | Implemented |
| 3 | Workspace CRUD/defaults | Implemented |
| 4 | Application discovery/reviewed launch | Implemented |
| 5 | Project import/detection/configuration | Implemented |
| 6 | Five starter templates, reviewed setup/history/recovery | Implemented |
| 7 | Independent controller/workers, output, readiness, Run & Preview | Implemented |
| 8 | Ordered workspace launch, editors/URLs, delays, switching | Implemented |
| 9 | Capture, restore, branching, explicit revision/history | Implemented |
| 10 | XDG Autostart, fixed/restore/hybrid, login grants | Implemented |
| 11 | Verified Save & Shutdown, cancellation/error state machine | Implemented; OS handoff tested with mocks |
| 12 | Dashboard, Activity, Settings, keyboard controls, responsive errors | Implemented; manual accessibility checks pending |
| 13 | Production binary, Debian package, launcher/icon/license/docs/screenshots | Implemented; clean-machine acceptance pending |

## Verified automatically

- Go race/vet, frontend tests and production builds.
- Owned-process readiness/output, duplicate prevention, stop/cancellation and controller reconnect.
- Native Wayland creation/configuration, session capture/branch and shutdown cancellation.
- Isolated dpkg unpack/removal, packaged production CLI/controller and native screenshots.

## Manual acceptance pending

- Real logout/login with all startup modes and an installed package.
- Real browser preview and editor handoff, GTK chooser, screen reader, keyboard/focus/scaling and X11.
- Explicit user approval before any real power-off test; preserve snapshots on OS denial.
- Clean-machine package installation/removal and retained user data.

## Practical limits

- Sessions capture known launch metadata plus supported desktop-scope and session D-Bus application
  identities. Unsupported external apps need explicit desktop/custom configuration.
- A review approves configuration, not mutable source. Restores never install dependencies.
- Unknown worker/descendant ownership blocks automatic retry and signalling.
  A verified released worker and absent process group can be explicitly reconciled.
- Runtime output keeps at most 64 KiB per run. Sessions/runtime lists stop at 256
  records and require explicit removal rather than silently deleting user history.
- Launch reviews are limited to 256 tasks and 4 MiB of reviewed metadata.
- Login startup is bound to a reviewed plan. Configuration changes require approval
  again. A recorded login dispatch is not replayed automatically after a crash.
- Closing the GUI preserves managed workers. Stop them before uninstalling/upgrading.
- Temporary scaffold output and partial setup require inspection; recovery never
  replays tools or automatically removes project/staging files.
