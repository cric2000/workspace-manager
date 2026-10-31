# Phase 4, unit 2: browsing installed applications

The Applications page now connects the discovery adapter to the desktop. Search by
name, description, generic name or desktop ID, select an application, and inspect
its launch metadata. Catalogue-page selection is local to that page. The
[workspace application editor](workspace-applications.md) now saves ordered choices
and duplicate preferences in profiles. [Unit 4](application-launching.md) now adds
reviewed individual desktop launches through a separate service.

## Code to study

1. `internal/applications/service.go`: read-only list and icon methods, using small
   interfaces at the platform boundary.
2. `internal/platform/linux/icons_linux.go`: GTK theme lookup and PNG conversion.
3. `main.go`: composes the service with the GIO catalogue and Wails dispatcher.
4. `frontend/src/services/applications.ts`: generated native calls, outside React.
5. `frontend/src/pages/Applications.tsx`: loading/retry, search, paging and details.

The details panel distinguishes desktop registration from successful launching.
It shows the stable ID, desktop file, terminal requirement and launch entry. A
D-Bus-activated application labels its Exec value as a fallback launch entry.
Quoted arguments and desktop field codes remain display metadata; nothing executes
or grants command approval on selection.

The page shows 24 entries at a time to limit simultaneous icon requests. Filtering
is local and returns to page one. Equal names retain separate IDs. Refresh hides
the previous list while discovery runs and clears a selection that disappeared.
Transport failures reveal a safe message and retry control. Responses from an
unmounted/older effect cannot replace the current catalogue.

## Icon boundary

The WebView sends only an installed application ID to `GetIcon`. Go re-resolves the
registration from the same GIO catalogue and requires it to remain menu-visible.
An invalid, missing or newly hidden application gets the generic fallback. Clients
cannot ask this method to load an arbitrary file or serialized GIcon reference.

The platform adapter accepts local file icons and themed icons. Themed icons resolve
through the active GTK display's icon theme; remote files and unsupported icon types
fall back. GTK calls run on the main desktop thread via Wails `InvokeAsync`. A buffered
result channel and context checks let a cancelled request return even if the main
loop stops; a late callback checks cancellation before using GTK.

Only lookup runs on GTK. The worker opens a local icon with nonblocking I/O, checks
that it is a regular file, and reads at most 2 MiB. Normal icon symlinks are allowed;
directories, FIFOs and devices are rejected. GdkPixbuf receives bytes without a file
or base URI, scales while loading, rejects dimensions above 8,192, and emits a static
PNG at most 64×64. Go verifies PNG dimensions and a 64 KiB output limit before
encoding a `data:image/png;base64` URL. Raw SVG, local file URLs and arbitrary source
bytes never become WebView image sources. Decoder time itself remains synchronous;
context checks surround it rather than claiming mid-decode interruption.

Four conversions can run concurrently. The page shares icon promises between rows
and details for each catalogue scan; refresh creates a new cache. Missing, corrupt,
unsupported or failed icons do not prevent browsing an otherwise valid catalogue.
An image decode failure in the WebView also returns to the generic icon.

No Go/npm dependency or system package was added. GTK4/GdkPixbuf and their normal
runtime loaders come from the existing native stack. This host uses GTK 4.22.4 and
GdkPixbuf 2.44.5, whose image loaders use Glycin helper processes. Tests need an
environment that allows those helpers' IPC; do not disable their sandbox to work
around an execution-environment restriction. The platform test process sets an
isolated cache root before GLib caches its environment, then removes that owned root.

## Build and checks

From the repository root:

```bash
export PATH="$PWD/.tools/bin:$PATH"
wails3 task test
wails3 build
./bin/workspace-manager
```

Focused commands after frontend assets and bindings have been generated:

```bash
go test -race ./internal/applications ./internal/platform/linux
go test -race ./...
go vet ./...
npm --prefix frontend test
npm --prefix frontend run typecheck
```

Do not rebuild the frontend in parallel with root Go checks: `main.go` embeds that
directory, and Vite replaces the assets during a build. The existing Taskfile orders
asset generation before Go tests. Restricted agent environments may require permission
to run the system decoder helpers; ordinary desktop test runs use their normal IPC.

The opt-in Wayland harness covers the new page and the previous workspace workflow:

```bash
go build -tags production,mcp -buildvcs=false -o bin/workspace-manager-smoke .
python3 scripts/native-smoke.py
```

It creates temporary desktop registrations and local/themed icon fixtures. In the
real WebKit window it checks search, selection, preserved launch metadata, decoded
PNG previews, minimum-width overflow and refresh after adding/removing registrations.
GIO monitor invalidation is asynchronous, so the harness waits for its updates through
the normal refresh button. The removed application's details must disappear. The
test endpoint is loopback-only with a random token, absent from the production build.
No installed desktop application, project command, autostart or shutdown is invoked.

## References checked

- [GTK icon lookup by GIcon](https://docs.gtk.org/gtk4/method.IconTheme.lookup_by_gicon.html)
- [GIcon serialization](https://docs.gtk.org/gio/method.Icon.to_string.html)
- [GdkPixbuf incremental loading and sizing](https://docs.gtk.org/gdk-pixbuf/class.PixbufLoader.html)
- Wails v3.0.0's local `pkg/application/mainthread.go` and installed GTK/GdkPixbuf headers.

Pixel-level layout, real OS keyboard input, screen readers, display scaling and X11
remain manual checks. Native automation provides behavioral/DOM evidence; it does
not establish those separate acceptance criteria or any launch/readiness behavior.
