# Implementation validation

Recorded on 2026-10-30. The first sections cover the desktop foundation;
later sections record storage and workspace-profile milestones.

## Observed environment

| Item | Observed value |
| --- | --- |
| Distribution | Ubuntu 26.04.1 LTS |
| Architecture | x86_64 / AMD64 |
| Desktop | ubuntu:GNOME |
| Session | Wayland |
| Go | 1.26.8 |
| Node | 24.21.0 |
| npm | 11.19.0 |
| Wails CLI / Go module / frontend runtime | 3.0.0 |
| GTK4 | 4.22.4 |
| WebKitGTK 6.0 | 2.52.6 |

`wails3 doctor` reports no required dependency issues. Legacy GTK3 and mobile SDKs
are not needed. The project uses pinned direct frontend dependencies and a committed
npm lockfile; `npm ls --depth=0` resolves without peer-dependency errors. Registry
audit reported zero known vulnerabilities at installation time, not a guarantee of
future security.

## Automated checks

- `bash -n scripts/check-environment.sh`: passed.
- `bash scripts/check-environment.sh`: passed.
- `go test -race ./...`: passed; five session-type/environment boundary cases.
- `go vet ./...`: passed.
- `npm --prefix frontend test`: all five Vitest tests passed.
- `npm --prefix frontend run build`: strict TypeScript and Vite build passed.
- `wails3 build`: Linux AMD64 production ELF built at `bin/workspace-manager`.

`wails3 task test` combines frontend asset generation/build, Go race tests, Go vet,
and Vitest. The root Go package needs the frontend assets because it embeds them.

## Native Wayland smoke evidence

A separate test binary was built from the same application with Wails' optional
`production,mcp` tags. A temporary Python standard-library harness started it with
`GDK_BACKEND=wayland`, a loopback-only test endpoint and a random bearer token.
The normal production build does not include this diagnostic endpoint.

Observed through the native WebKitGTK WebView:

- One Linux/AMD64 window loaded the embedded React UI.
- The generated Go bridge connected and reported Linux/AMD64 and Wayland.
- The initial 1160-pixel viewport had no horizontal overflow.
- Clicking the appearance control changed the applied theme.
- Refreshing diagnostics completed through the generated native bridge.
- Closing the final window exited the GUI with status 0.

The harness initially needed corrections for suppressed production info logs,
asynchronous React updates and the expected HTTP disconnect when closing the last
window. The final run passed all checks. No application workaround was needed.
The harness terminated only its own test process on failed attempts.

This is behavioral/DOM evidence, not pixel-level screenshot review. No managed
project, autostart entry, persistent application config or real shutdown was used.

## Known upstream diagnostics

The installed GTK4 headers produce deprecation warnings in Wails v3.0.0's X11 helper
code (`gdk_x11_display_get_xdisplay` / `gdk_x11_surface_get_xid`). They do not prevent
compilation. WebKit may print its signal-10 handler notice at native startup. The
native smoke checks completed successfully with that notice present.

Do not patch downloaded module-cache files or suppress all compiler warnings.
Re-evaluate the warnings when intentionally updating the pinned Wails release.

## Remaining manual checks

Run `./bin/workspace-manager` on the target desktop, then check:

1. Light/dark rendering, text contrast and layout at minimum window dimensions.
2. Tab navigation, visible focus and keyboard activation of both controls.
3. GNOME window decorations, resizing and display scaling.
4. `wails3 dev` hot reload in an interactive development session.
5. X11 compatibility in a real X11 session; it was not tested here.

The controller and process persistence do not exist in Phase 1. Their GUI
close/reopen acceptance gate belongs to Phase 7. Actual shutdown needs separate
manual approval in Phase 11 and must never be part of these checks.

Git was initialized on branch `main`. No commit or remote was created.

## Phase 2, unit 1 validation

The initial domain records and XDG resolver were added without new dependencies,
frontend changes or configuration writes. Phase 2 storage repositories are pending.

Passed:

- `go test -race ./internal/domain ./internal/storage`
- `go test -race ./...`
- `go vet ./...`

Tests cover stable IDs; malformed identities, names, timestamps and paths; project
references; JSON round trips; branching without shared slice mutation; multiple
branch generations; invalid source snapshots; fixed/restore/hybrid autostart
selections; XDG defaults and independent absolute overrides; relative-value
fallbacks; missing HOME; and resolution without directory creation.

The native window was not rerun for this pure Go unit. The Phase 1 native evidence
and outstanding manual checks above still apply. See
[domain-foundation.md](domain-foundation.md) for code responsibilities and limits.

## Phase 2, unit 2 validation

Added versioned codecs and Linux atomic replacement with no new dependencies or
GUI changes. All filesystem tests use isolated temporary directories.

Passed:

- `go test -race ./internal/storage`
- `go test -race ./...`
- `go vet ./...`

New tests exercise all four domain record kinds, a schema-1 fixture, rejected
corruption/unknown fields/duplicate aliases, input limits, explicit ordered
migrations, migration failures, last-good preservation before commit, verification
failures, committed-but-not-durable errors, file permissions and ownership,
symlink/directory/FIFO rejection, pinned-root path replacement and concurrent
complete-file reads/writes.

An internal-symlink test exposed that `os.Root.OpenFile` followed an in-root link
despite the requested no-follow flag. The Linux read implementation now uses
descriptor-relative `openat` to enforce `O_NOFOLLOW`; internal and external symlink
tests pass. Test setup explicitly creates a `0700` app directory rather than
assuming that the test runner's temporary root is already private.

No real power-loss test, cross-process transaction isolation, backup recovery or
new native-window check is claimed. Those concerns are either repository work or
unchanged desktop checks. See [storage-format.md](storage-format.md) for guarantees
and commit-error semantics.

## Phase 2, unit 3 validation

The initial-model storage milestone is complete. Repository operations remain
backend-only; the native UI still shows foundation diagnostics. No dependencies
were added and no real user configuration or project directory was changed.

Passed:

- `go test -race ./internal/storage`
- `go test -race ./...`
- `go vet ./...`

New integration checks cover workspace CRUD and undelete, project source-directory
preservation, stale edit/delete conflicts, explicit session revisions, independent
branches, cyclic ancestry rejection, malformed-primary quarantine/recovery,
fingerprint-verified history, failed backups, cancellation and close behavior,
filename/ID mismatches, refusal to overwrite newer schemas, and exact pre-migration
bytes in history.

The separate-process test starts two instances of the storage test executable;
their 20 settings increments are preserved. A per-instance test writer also injects
errors after real history and primary writes to verify that repository commit
status describes the primary, not a nested backup commit.

Private-directory tests verify ancestor creation, `0700` modes, no automatic chmod
of existing paths, symlink refusal and cancellable lock acquisition. All fixtures
live below test-owned temporary directories. Rooted record opening was factored
into one checked helper shared by bounded reads and backup sync; previous no-follow
and atomic-write tests continue to pass.

The GUI and frontend were not changed or rerun in this unit. Phase 1's native
evidence and remaining desktop manual checks still apply. Real power loss and
multi-repository transactions are not claimed. See [repositories.md](repositories.md)
for the API contract and limitations.

## Phase 3 validation

Workspace profiles now persist through the native UI. This milestone adds no
dependencies. Go owns identity, validation, persistence and reference guards;
React uses generated typed calls and safe result messages.

Passed on the same GNOME Wayland host:

- `go test -race ./...` and `go vet ./...`.
- Fourteen Vitest tests across profile workflows and desktop diagnostics.
- Strict TypeScript checking and the Vite production build.
- Wails Linux AMD64 production build at `bin/workspace-manager`.
- `scripts/native-smoke.py` against the separate `production,mcp` test binary.

Service tests use real isolated repositories for CRUD, revision conflicts,
reference-preserving edits/duplication, guarded default deletion, settings revision
conflicts, retained unrelated preferences, autostart-required defaults, cancellation,
corrupt settings preservation/retry, shutdown and reopen. Concurrent operations
through two independent service instances preserve the default reference. Storage's
separate-process flock tests from Phase 2 continue to pass.

Frontend tests cover creation and pending-write controls, editing with revisions,
duplication, default selection/clearing, confirmation/cancel, retained conflicting
drafts, no silent rebase after reload, unknown transport outcomes, committed-error
reconciliation, initial storage failure/retry, search, local validation, focus return,
navigation, themes and diagnostics. jsdom uses a minimal open/close dialog shim;
native modal behavior is not inferred from those component tests.

The native harness opens two consecutive windows using the same temporary XDG
config/state/cache roots. It observed create, rename, duplicate, default selection,
guarded default deletion, confirmed deletion, stable identities and persisted
profile/default data after clean close/reopen. It also verified modal top-layer
state, initial name focus, inert background, cancellation handling, focus restoration
after cancellation and deletion, theme changes, diagnostics and no horizontal
overflow at the minimum-width native viewport. Both GUI processes exited with 0.
On-disk JSON was inspected after closing. No real user configuration, project
command, autostart entry or shutdown was touched.

Native checks caught two focus issues: React autofocus ran before the dialog was
shown, and deletion cleanup tried to return focus before its trigger was removed.
Focus is now applied after `showModal` and restored after the DOM commit. The final
full native run passed. The harness also needed to unmaximize the window before
requesting its minimum size under GNOME.

This remains behavioral/DOM evidence. Pixel-level review, real OS Escape/Tab keys,
screen-reader behavior, display scaling, hot reload and X11 remain manual checks.
MCP's synthetic key events do not prove compositor-level keyboard behavior. Theme
selection is per-window for now. Project/application editing, launching, controller
process persistence and later product phases are not implemented by this milestone.

## Phase 4, unit 1 validation

Added a read-only desktop catalogue through GIO Unix 2.88.0, already supplied by
the host's native development stack. No Go/npm dependency or system package was
installed. This unit does not change frontend code, bindings, persistent domain
records or the native GUI. Phase 4's picker, profile application configuration,
launching and instance behavior remain pending.

Passed:

- `go test -race -count=1 ./internal/applications ./internal/platform/linux`.
- `go test -race ./...` and `go vet ./...`.
- `desktop-file-validate internal/platform/linux/testdata/org.example.Editor.desktop`.
- `bash -n scripts/check-environment.sh` and its read-only prerequisite check.
- Opt-in host discovery/resolution with `WORKSPACE_MANAGER_DESKTOP_SMOKE=1`.

Fixture integration tests run the real GIO adapter in fresh test subprocesses with
isolated XDG paths and fixed locales/desktops. They cover user/system precedence,
minimal Hidden tombstones, malformed higher-priority entries, nested IDs, duplicate
display names, visibility/current-desktop ordering, locale fallback, escaped strings,
quoted launch metadata/field codes, executable/PATH checks, D-Bus-only entries,
symlinked desktop registrations, empty results and cancellation. A fixture program's
sentinel remained absent, and its desktop metadata remained byte-for-byte unchanged.

An early fixture showed that `g_desktop_app_info_new(id)` could resolve a lower
system entry behind a minimal user Hidden override. Lookup now searches GIO's
registered list instead; the masking and no-fallback regression checks pass.

On the actual host, the adapter discovered and resolved 32 visible desktop
applications without launching them. The check logged only the count. GIO needs no
display connection for these calls. This observation establishes catalogue behavior,
not launch success, availability of a D-Bus service, running instance detection or
readiness. Existing Phase 3 native-window evidence still applies; no new GUI/native
launch test was needed for this backend-only unit.

GIO's enumeration is synchronous and does not expose per-file parse failures.
Cancellation checks cannot interrupt an in-progress native scan. GLib environment
caching is handled through subprocess isolation in tests; live cache/refresh behavior
must be checked when adding the picker. See [desktop-discovery.md](desktop-discovery.md)
for responsibilities, limitations and exact focused commands.

## Phase 4, unit 2 validation

Added the read-only Applications service/page and GTK/GdkPixbuf icon adapter.
No Go/npm dependency, system package or persistence-schema change was needed.
The existing native stack provides GTK 4.22.4 and GdkPixbuf 2.44.5.

Passed:

- `go test -race ./...` and `go vet ./...`.
- Twenty-four Vitest tests and strict TypeScript checking.
- Wails-generated bindings for three services/nine methods and production build.
- Read-only environment checks, including GIO Unix and GdkPixbuf versions.
- The complete native Wayland harness with catalogue and workspace workflows.

New Go tests verify safe list errors, visible-registration checks, ID-only icon
requests, bounded PNG results, malformed/oversized images, local SVG/PNG rendering,
non-local reference rejection, FIFO/directory refusal, four-worker concurrency and
cancellation while waiting for GTK dispatch. Decoder caches use a test-owned
temporary root, initialized before GLib caches its environment.

GdkPixbuf's Glycin helper initially failed inside the agent execution sandbox with
an IPC `Operation not permitted` error. The same render test passed with authorized
helper execution; no decoder sandbox was disabled. Subsequent Go checks ran with
that permission. An initial root Go check overlapped Vite replacing embedded assets;
it was rerun successfully after the build completed. The Taskfile already orders
those dependent steps correctly.

New frontend checks cover loading/disabled refresh, search by metadata/ID, separate
selection for duplicate names, launch-entry details, empty catalogue versus empty
search, safe discovery/transport errors, retry, pagination, shared PNG requests,
broken/unsupported icon fallback, refresh/removal and responses after navigation.
The refresh handler clears old entries before replacing its icon cache so stale
rows cannot issue new icon requests during that transition.

Native WebKit checks used temporary data/config/state/cache directories and two
test desktop registrations with local SVG and themed icons. Search selected the
intended stable ID, preserved quoted launch metadata, and displayed decoded PNG
images with nonzero natural dimensions. At the minimum native window width there
was no horizontal overflow. Refresh observed a registration added while the GUI
was open and then removal of the selected registration; obsolete details cleared.
GIO's file-monitor invalidation was allowed to propagate asynchronously through
the real desktop main loop. No desktop application was launched.

The previous native workspace CRUD, default guards, modal focus, theme, diagnostics,
metadata persistence after close/reopen and clean GUI exits also passed. Test
profile/desktop/icon files stayed within temporary roots. The normal production
binary remains at `bin/workspace-manager`, without the optional MCP endpoint.

This is behavioral/DOM evidence. Pixel review, real keyboard navigation, screen
readers, scaling and X11 remain manual checks. Application selection is currently
page-local; workspace application settings, duplicate preferences, launching and
custom executables remain pending. See [application-catalogue.md](application-catalogue.md)
for the code walkthrough and exact commands.

## Phase 4, unit 3 validation

Workspace profiles now save ordered application IDs and duplicate-launch
preferences. This unit adds no dependency or application-launch operation.
Workspace schema 2 has an explicit schema-1 migration; settings, projects and
sessions remain at schema 1.

Passed:

- `go test -race ./internal/domain ./internal/storage ./internal/workspaces`.
- Full `go test -race ./...` with permission for the normal decoder helpers.
- `go vet ./...`, strict TypeScript, and all 31 frontend tests.
- Generated bindings: three services, ten methods and the duplicate-policy enum.
- Linux AMD64 production build and the complete native Wayland harness.

Domain/service checks cover ID/policy validation, the 64-entry limit, ordered
persistence, independent slice copies, preservation during name edits, unavailable
saved references, invalid/unavailable additions, stale revisions, explicit clearing,
profile duplication and close/reopen. A real schema-1 JSON fixture verifies that
reading leaves the original file unchanged and an explicit save writes schema 2
only after archiving the exact legacy bytes. Unknown old fields, premature
application fields in schema 1, duplicate JSON keys and unsupported newer versions
are rejected. Other record kinds retain their previous format.

Seven additional frontend workflows cover add/search/reorder/preferences, cancel,
retained missing references, explicit removal/clear, conflict drafts without silent
rebasing, catalogue failure/retry, and correction when an added application disappears.
The previous catalogue, diagnostics and profile workflows continue to pass.

The native harness saved two test desktop IDs, moved one before the other and set
different duplicate policies. On-disk JSON confirmed schema 2 and exact list/policy
values in the original and duplicated profile. After removal of the test-owned
editor registration and clean GUI close/reopen, its reference remained visible as
unavailable. Saving that retained reference succeeded; removing it from the copied
profile left the original unchanged. Both GUI processes exited cleanly. Existing
default guards, profile CRUD, catalogue refresh, PNG icons and diagnostics also
passed. All profile, desktop and icon fixtures remained under temporary XDG roots.
No installed application, project command, autostart or shutdown was invoked.

Earlier native attempts exposed harness timing issues around field selection and
GNOME/WebKit configure events. Field replacement now uses a complete controlled-input
update; the frontend tests still exercise ordinary typing. Layout checks run after
form interactions and wait for a stable viewport. In the final run a narrow
approximately 760-CSS-pixel viewport used native WebView zoom; both page and picker
had no horizontal overflow. This does not claim that the physical window was resized.
OS keyboard input, physical resizing, pixel-level review, screen readers, scaling
and X11 remain manual acceptance checks.

Saving preferences grants no launch approval and does not implement running-instance
checks. Reviewed launching, custom executable configuration and conservative
instance checks remain pending. See [workspace-applications.md](workspace-applications.md)
for responsibilities, migration details and exact build/test commands.

## Phase 4, unit 4 validation

Reviewed individual application launching is implemented through GIO. No dependency
or persistence format changed. The pinned Wails production build passed, including
generated bindings and the TypeScript/Vite build. The full Go race suite and vet
passed; all 39 frontend tests passed.

Service tests verify that review does not execute, invalid/missing tokens cannot
launch, one token dispatches once, simultaneous requests report in-progress, and
completed outcomes can be retrieved without dispatching again. Expired reviews,
bounded review capacity, cancellation and safe failure messages are covered.

Fresh isolated GIO subprocess fixtures launched only a test-owned short-lived
argument-capture helper. They confirmed quoted arguments, literal shell punctuation,
`%%`, `%c`, `%k`, no-file `%f`/`%u` expansion and `Path` with spaces. A desktop-file
edit between review and launch, a Hidden tombstone and a cancelled context prevented
execution. An invalid working directory returned a launch error. Fingerprinting
tests reject oversized/nonregular files and include effective metadata in the hash.

The real native Wayland bridge/modal workflow passed in test-owned XDG roots:

- Review initially focused Cancel and displayed the unknown-running-state warning.
- Cancel did not create the helper's capture file.
- Confirm returned the desktop handoff result and the helper captured exactly one
  `two words` argument; the desktop `%U` field code was handled by GIO.
- Appending a desktop extension after another review prevented a second launch and
  displayed the changed-entry message. The capture remained unchanged.
- Catalogue discovery, icon rendering, live add/remove refresh, workspace CRUD,
  ordered application preferences, independent duplication and close/reopen passed.
- Both GUI processes exited cleanly; persistent test data stayed under temporary
  XDG directories. No installed user application, project command or shutdown ran.

Frontend tests additionally cover interrupted responses and result checks using the
same token, busy controls, retries that obtain a fresh review, cancellation and
discarding stale review responses. These checks do not claim a durable cross-GUI
deduplication guarantee.

This run again used WebView zoom to establish the narrow CSS viewport; physical
resizing remains manual. Installed graphical applications, D-Bus activation,
Terminal=true behavior, focus policy, early startup failures, OS keyboard navigation,
screen readers, pixel/scaling review and X11 remain manual checks. A successful GIO
handoff does not establish readiness. Workspace execution, custom executables and
supported running-instance checks remain pending. See
[application-launching.md](application-launching.md) for the walkthrough and commands.

## Phase 4, unit 5 validation

Workspace schema 3 adds custom executable configurations with literal argv, required
absolute executable/working-directory paths, a stable workspace-scoped ID and an
optional well-known D-Bus identity. Reads migrate schemas 1 and 2 in memory; explicit
saves preserve exact old bytes in history before writing schema 3. Other record
kinds retain schema 1. No dependency version changed; the existing pinned Godbus
module became a direct test dependency for private-bus ownership fixtures.

The production Wails build, Go race suite and vet passed. All 43 frontend tests
passed. New backend coverage includes configuration bounds, empty/literal arguments,
invalid identities, exclusive desktop/custom targets, duplicate custom IDs, independent
cloning, revision-matched resolution, persistence/reopen, stale approval rejection,
executable metadata changes, cancellation, and precommit rejection of caller-assigned
new IDs or drafts whose JSON escaping exceeds the document budget. Schema-2 fixtures
verify desktop choices survive migration, reads preserve bytes, history contains
exact originals and old envelopes cannot smuggle custom command fields.

Custom-launch integration tests use a test-owned helper and verify a path containing
spaces, literal `; $HOME`, literal `%U`, an empty argument and the explicit working
directory. No shell/PATH expansion occurs. Missing paths, changed executables,
non-executable files and cancelled requests do not dispatch.

The instance-check integration starts a private `dbus-daemon` with an isolated
configuration and no activation services. A test-only Godbus connection acquires
and releases a well-known name. GIO checks report absent → owned → absent; cancelled,
unreachable-bus and empty-address checks report unknown. Checks use private GIO
connections with cancellation/timeouts; they do not autolaunch a bus or change the
GUI's shared session-bus lifecycle. Service tests verify both duplicate policies,
confirmation-time rechecking, skipped/unknown outcomes with no dispatch and no
process-name fallback.

The native Wayland workflow used temporary XDG roots and test-owned helper files:

- Existing desktop review/cancel/confirm, changed-registration protection, catalogue
  refresh, PNG icons, workspace CRUD/default guards and independent copies passed.
- It added a custom tool with a literal `literal ; $HOME %U` argument and an empty
  argument. Keeping/saving its draft did not execute it.
- A saved SkipIfRunning review with no identity returned `instance_unknown` and
  left the capture unchanged. Selecting AlwaysLaunch, obtaining a new review and
  confirming executed the helper with both arguments exactly preserved.
- A third GUI process reopened the saved custom form with both arguments intact.
  Its editor and the existing picker passed narrow native CSS viewport checks.
- All three GUI processes exited cleanly. Persistent test data stayed in temporary
  XDG directories; no installed user application, project command or shutdown ran.

Two earlier native runs stopped on a one-CSS-pixel difference between integer
`scrollWidth` and `innerWidth` at fractional zoom. Diagnostics showed equal dialog
scroll/client widths and no content outside the viewport. Layout checks now allow
that one-pixel rounding difference and retain element-bound diagnostics on failure.
The final run did not report a zoom fallback, but physical resizing and OS keyboard
behavior still require manual observation.

Installed graphical application startup, D-Bus activation, Terminal=true launch,
focus, screen readers, pixel/scaling review and X11 remain manual gates. Bus ownership
is a point-in-time configured service identity, not a particular process/window,
browser profile or readiness guarantee. Ordered workspace execution and managed
project supervision remain later phases. See [custom-applications.md](custom-applications.md).

## Phase 5, unit 1 validation

Read-only project inspection is available on the Projects page. It returns bounded
metadata/source evidence, candidate argv, required-tool PATH presence, ambiguity
findings and partial-result status. It has no registration, command-approval,
installation or execution operation. Project and session schemas remain version 1;
workspace schema remains 3. Three metadata parsers were pinned after checking their
APIs: BurntSushi TOML 1.6.0, go.yaml.in/yaml/v3 3.0.4 and x/mod 0.41.0. The Go build
fetched their module metadata and the YAML parser's pinned test dependency; it did
not install project/framework dependencies or run managed project code.

The Wails production build, Go race suite and vet passed. All 48 frontend tests
passed. Focused race checks also passed after the final main-signature/context
refinements and extra malformed-text/resource-boundary fixtures.

Backend fixtures cover npm/pnpm/Yarn/Bun evidence (including binary Bun lockfile
presence), explicit scripts, conflicting/no managers, root workspace declarations,
duplicate-key/trailing/invalid JSON and unsupported field types. Go fixtures find
two distinct main packages while excluding test files, unmatched build tags,
dependency directories and nested modules. Python fixtures inspect requirements,
Django conventions, explicit FastAPI and console entrypoints without importing source.
Cargo fixtures cover binaries, automatic discovery, required feature flags and
workspace-only manifests. Compose/Make fixtures exercise file alternatives,
duplicate services, static targets, comments and assignments without evaluating code.

Filesystem fixtures verify absolute/canonical roots, paths with spaces, selected
root symlinks, refused metadata/source/intermediate symlinks, FIFO metadata and
FIFO selected-root paths without blocking, oversized files,
UTF-8/NUL rejection, malformed TOML/YAML, nesting complexity, depth, file count,
total bytes and directory-entry limits. A fake executable on PATH is discovered
without being invoked; tool, script and Make sentinels remain absent. Metadata bytes
remain unchanged. Service tests verify safe messages, two active slots and cooperative
cancellation. UI tests cover evidence and exact argv, no approve/run actions, errors,
duplicate-request prevention, cancellation, explicit rescan and discarded late responses.

The native Wayland workflow ran four GUI processes in temporary XDG roots, retaining
the previous profile/application checks. The new fourth-window inspection fixture:

- Detected Next.js/TypeScript package metadata and a nested Go main package.
- Displayed the exact `pnpm` argument list `run`, `dev`, then the Go candidate path.
- Kept old results until an explicit rescan after the test changed a script from
  `dev` to `start`; the rescan displayed the new candidate and removed the old one.
- Returned an actionable directory error when given the package.json file path.
- Passed the narrow CSS viewport layout check after another successful inspection.
- Did not expose the fixture's `.env` contents, create a project repository, run a
  project command or alter any project file bytes. All GUI processes exited cleanly.

Candidate confidence describes evidence, not successful compilation, environment
correctness or readiness. Python source inference, richer monorepos, inherited
workspace data, full Compose/Make semantics and tool versions/plugins remain
unverified. Native folder selection, reviewed registration/configuration persistence
and rescan-to-approved-config comparison are the next units. OS keyboard behavior,
physical resizing, pixel/scaling review, screen readers and X11 remain manual gates.
See [project-inspection.md](project-inspection.md) for the walkthrough and commands.

## Phase 5, unit 2 validation — 2026-10-31

Reviewed canonical project registration and core startup configuration are now
available. The chooser, inspection, draft, review and commit operations are distinct.
Project/session schema 2 adds command snapshots and configuration-bound approval;
settings remain schema 1 and workspaces remain schema 3. Saving starts no project
process. Auxiliary project settings and workspace association remain pending.

Validation passed:

- Focused domain/storage/project race tests, then `go test -race ./...`.
- `go vet ./...` and all 56 frontend tests across seven test files.
- Generated Wails bindings, TypeScript checking, production frontend build and
  the pinned Wails native production build. The final production executable was
  rebuilt after the last backend review-expiry refinement.
- The production+mcp test binary and the expanded native Wayland smoke workflow
  across five GUI processes with isolated temporary XDG roots.

New backend fixtures verify approval mismatch for every command field, nil/empty
argv normalization, command limits, independent session-branch arguments and
approval pointers, strict project/session schema-1 migrations (including nested
unknown command fields), exact old-byte project history and unsupported versions.
Registry fixtures cover no write during review, canonical alias resolution,
competing service handles, stable command IDs, stale review/edit revisions,
changed directories/executables, expiry, bounded token retention, same-token
replay with independent returned values, save/reopen, fail-closed corrupt/newer
storage, safe errors and single-dialog chooser/cancellation adapters. PATH lookup
finds a test-owned executable without running it. Normal source edits do not
invalidate approval of unchanged configuration. Project command sentinels remain
absent.

New frontend workflows verify directory selection without automatic inspection,
cancelled selection preserving the path, suggestion/custom/no-command drafts,
literal and empty argument rows, ordering/removal, explicit review before saving,
revision/ID preservation without carrying approval into a draft, Back requiring a
new review, preserved blocked conflicts, duplicate-save prevention, same-token
outcome checks after transport loss, changed-review recovery and storage errors.
Review/edit transitions move focus to the displayed stage.
The final transport-recovery fixture additionally verifies that an expired or lost
review after a lost save response requires closing/reloading, rather than offering
another mutation from an uncertain draft.

The native workflow retained the prior workspace/application checks and added:

- Inspected a polyglot project, rescanned changed metadata and selected its Go
  candidate. The reviewed executable resolved to an absolute path.
- Verified no project record existed before explicit approval, including while
  the first review was displayed. Back preserved the editable draft.
- Replaced the draft executable with a test-owned project helper, reviewed literal
  `literal ; $HOME %U` and empty arguments, then saved one schema-2 registration.
- Closed/reopened the GUI and found the saved arguments intact in the editor.
- Changed the executable while its review was open. Save returned a changed-review
  error and the exact saved project bytes stayed unchanged. A fresh review saved
  successfully; the existing registration was updated.
- Passed narrow CSS viewport checks for the registered-project list and reopened
  configuration dialog. Native WebView zoom was needed in one earlier window;
  physical resizing remains a manual acceptance check.
- Preserved every project file byte (apart from the test's intentional manifest
  edit), exposed no fixture `.env` values, and left the project execution sentinel
  absent. All five GUI instances exited cleanly.

The first Wails build needed network access for pinned Go test-dependency metadata;
the permitted retry fetched it. The first full race suite was blocked by sandbox
restrictions on the existing image decoder IPC/private D-Bus fixtures; the run with
access to those facilities passed. Wails emitted upstream deprecated X11 API
warnings during a fresh CGO compilation; the build succeeded.

Native OS directory selection/cancellation is wired through the pinned SDK and
covered through an injected adapter, but direct GTK chooser interaction has not
been manually verified. OS keyboard behavior, screen readers, pixel/scaling review,
physical resize and X11 remain manual gates. Approval covers saved configuration,
not future source/dependency contents or successful execution. No managed project
command, dependency installation or shutdown ran. See
[project-registration.md](project-registration.md) for code and exact commands.

## Phase 5, unit 3 validation — 2026-10-31

Projects now save per-command environment-file references, readiness, delay and
graceful stop/action configuration, plus project preview/editor preferences. The
editor and review show these settings explicitly. Their runtime behavior remains
Phase 7 work; no project/editor/stop action, endpoint probe or environment-file load
was added. Projects/sessions use schema 3; workspace/settings schemas stay 3/1.

Passed on the final implementation:

- `go test -race ./...`, with access to the existing local image-decoder IPC and
  private D-Bus fixtures; focused settings/domain/storage race checks also passed.
- `go vet ./...`, generated bindings, TypeScript checking and the Wails production
  build. No new dependency was introduced.
- All 59 frontend tests in seven test files.
- The native production+mcp binary and
  `python3 scripts/native-smoke.py --projects-only`: two isolated Wayland GUI
  instances tested project inspection/settings, save/reopen and changed reviews.
- The existing workspace/application native checks completed successfully during
  the earlier full-workflow runs in this validation session. The final focused
  run exercised the project flow independently, with no saved workspace prerequisite.

Backend tests cover the complete runtime/preference fingerprints, URL schemes and
credentials, explicit loopback HTTP/TCP checks, port/status/timing bounds, distinct
project-relative environment paths, delay limits and permitted stop signals/grace.
An environment file with mode 000 can be referenced through metadata-only checks;
its secret contents never enter a review or saved JSON. Symlink leaves/parents,
FIFOs, directories and escapes are refused. Missing references are allowed, but
replacement with an unsafe reference before save invalidates review. Editor-only
and stop-only executable changes invalidate review independently of startup argv.
No executable sentinel ran. Oversized escaped argument/settings documents fail
before registration; the final approved-document budget is also checked before
writing. Nested settings and approval data remain independent across review
responses, input drafts, saved records and session branches.

Migration fixtures independently compute a historical schema-2 command fingerprint
and verify it survives both project and embedded-session migration. New runtime or
preference fields smuggled into a schema-2 envelope are rejected. Reads preserve
the exact original bytes; explicit edits archive them before schema-3 output. No
new runtime option or preference approval is fabricated.

The new frontend tests configure all settings, inspect their exact review values
and confirm that only the review token is submitted to save. Reopened nested
settings can be edited without mutating the displayed saved record. Approval fields
are excluded from drafts. Switching HTTP to TCP clears inactive HTTP fields;
clearing stop/runtime settings removes their dependent action/configuration.

The final native project workflow:

- Inspected and rescanned the existing mixed-language fixture, preserving source
  bytes except the test's intentional package manifest change.
- Reviewed the Go candidate, returned to edit, then configured a test-owned
  executable with literal and empty argv, `.env` reference, three-second delay,
  HTTP 200 readiness, preview URL, editor action and SIGINT stop action/grace.
- Confirmed no project record existed before explicit approval and that the review
  contained the settings without exposing fixture `.env` contents.
- Saved one schema-3 record with startup and preference approvals. Its persisted
  paths, delay, stop signal, editor argv and preview URL matched the draft; no
  environment contents were copied.
- Closed/reopened the GUI, verified the saved settings/arguments, rejected a changed
  executable without modifying the saved bytes, then saved after a fresh review.
- Passed narrow CSS viewport layout checks for the list and expanded settings
  editor. Both GUI instances exited cleanly; the execution sentinel stayed absent.

Earlier native attempts stopped on test-harness issues: a numeric string returned
through the MCP helper was parsed as a number, and a conditional stop-action field
was filled before React mounted it. Assertions now compare numeric input inside
the WebView, and conditional controls have explicit mount waits. The focused mode
reuses the same project workflow as the complete script. Native WebView zoom was
needed in some earlier windows; physical resize remains a manual check.

Native GTK chooser interaction, OS keyboard/screen-reader behavior, pixel/scaling
review and X11 remain manual gates. Readiness settings do not establish readiness;
reference approval does not freeze file contents. Rescan comparison, workspace
association and the full Phase 5 workflow gate remain pending. See
[project-settings.md](project-settings.md) for code responsibilities and commands.
