# Phase 4, unit 1: desktop application discovery

This unit reads the installed desktop catalogue. It does not modify a workspace,
launch a desktop application or execute a project command. The Phase 3 GUI remains
usable. [Unit 2](application-catalogue.md) now adds the read-only catalogue UI;
[unit 3](workspace-applications.md) adds saved workspace application settings.
Launch workflows remain pending.

## Responsibility and code

`internal/applications/desktop.go` defines toolkit-independent metadata and validates
desktop IDs. An ID such as `org.gnome.Terminal.desktop` is the application identity;
the display name can change with the user's locale. Legacy names are accepted too.
Paths, URI strings and command arguments are not accepted as IDs.

`internal/platform/linux/desktop_linux.go` is a small cgo adapter around GIO:

- `Discover(ctx)` returns a sorted list of menu-visible registrations.
- `Lookup(ctx, id)` resolves the same installed catalogue by ID, including valid
  non-menu entries. Missing or masked registrations return `ErrUnavailable`.

Metadata includes localized display/generic names, description, stable ID, source
desktop file, icon reference, commandline/executable labels and terminal/D-Bus flags.
These are point-in-time registrations, not process records or readiness results.
An unavailable `TryExec`/executable can exclude a registration. Conversely, a valid
D-Bus-activated registration may have no `Exec` field at all. Presence alone does
not prove that launching will succeed.

The adapter leaves XDG precedence, nested desktop IDs, localized values, escaping,
Hidden/NoDisplay/OnlyShowIn/NotShowIn and executable lookup to the existing platform
library. It handles ordinary symlinked desktop entries like the desktop itself;
it neither provisions directories nor writes metadata. We avoid maintaining another
partial desktop-entry parser or parsing `Exec` as a shell program.

The `Icon` field is GIO's serialized `GIcon` reference. It is not an image, arbitrary
HTML or a URL that React should embed directly. Icon resolution/rendering belongs
in [unit 2's adapter/UI](application-catalogue.md). `Commandline` retains desktop field codes and quoting
for display. It must not be split with a shell or run as a single executable name.
Use the platform launch API when implementing launch behavior.

## A precedence trap covered by tests

A user can mask a system application with a minimal file containing only
`[Desktop Entry]` and `Hidden=true`. On this host's GLib 2.88.0, the registered list
honors that override, while `g_desktop_app_info_new(id)` can reject the minimal user
file and construct the lower-priority system application instead.

`Lookup` therefore searches `g_app_info_get_all()`, just like discovery. This also
prevents fallback through a malformed higher-priority override. The fixture test
requires a masked application to remain unavailable. Do not reintroduce the direct
constructor as an unchecked shortcut when implementing launch.

## Memory, lifecycle and limits

GIO returns owned GObject references. The adapter copies borrowed strings to Go,
frees allocated icon strings and releases every object/list, including on cancelled
calls. No C pointer escapes into application metadata.

GLib caches XDG and locale environment values process-wide. Treat the login
environment as fixed; do not temporarily change it around calls. The adapter needs
no GTK initialization or display connection. Tests use fresh subprocesses so fixture
environments cannot contaminate one another or the real desktop catalogue.

Cancellation is checked before/after native enumeration and during Go conversion;
cancelled calls discard their result. The synchronous GIO enumeration itself cannot
be interrupted by a Go context. GIO also omits invalid files without supplying
per-file parse errors through this API. This catalogue is not a strict desktop-file
validator or proof that every installed entry is well formed. Its cache/monitor
behavior and UI refresh were subsequently verified in unit 2's native desktop
checks, including adding and removing test-owned registrations while the GUI ran.

The adapter returns valid non-menu metadata through `Lookup` with `Visible=false`;
only `Discover` applies menu filtering. This distinguishes registration availability
from whether an application belongs in a general picker. It grants no launch approval
and makes no claim about existing windows, instance identity or duplicate prevention.

## Verification

The existing native toolchain supplies `gio-unix-2.0` through `libglib2.0-dev`. No
Go or npm dependency was added. `scripts/check-environment.sh` now checks its version.

From the repository root:

```bash
go test -race ./internal/applications ./internal/platform/linux
go test -race ./...
go vet ./...
desktop-file-validate internal/platform/linux/testdata/org.example.Editor.desktop
bash scripts/check-environment.sh
```

An optional read-only host check discovers and resolves the actual installed
applications, logging their count without their names, paths or launch arguments:

```bash
WORKSPACE_MANAGER_DESKTOP_SMOKE=1 go test -race -count=1 \
  -run '^TestInstalledDesktopCatalogue$' -v ./internal/platform/linux
```

Fixtures cover user/system precedence, Hidden tombstones, malformed overrides,
nested IDs, equal display names, menu visibility and current-desktop ordering,
locale fallback, escaped text, preserved quoted launch metadata and field codes,
missing/non-executable `TryExec`, missing executables, PATH lookup, D-Bus-only entries,
symlinked registrations, empty catalogues and cancellation. A fixture executable
would write a sentinel if run; discovery leaves the sentinel absent and the source
desktop file unchanged. No application is launched by these checks.

## References checked

- [Desktop Entry Specification](https://specifications.freedesktop.org/desktop-entry/latest-single/)
- [GIO registered applications](https://docs.gtk.org/gio/type_func.AppInfo.get_all.html)
- [GIO menu visibility](https://docs.gtk.org/gio/method.AppInfo.should_show.html)
- [DesktopAppInfo API](https://docs.gtk.org/gio-unix/class.DesktopAppInfo.html)
- [Executable metadata and launch guidance](https://docs.gtk.org/gio/method.AppInfo.get_executable.html)

Current web references describe newer GLib documentation. The APIs used here were
also checked against the installed GLib 2.88.0 headers and exercised by the fixtures.
