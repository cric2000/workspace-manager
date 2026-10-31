# Reviewed desktop launching

Phase 4, unit 4 adds **Review launch** to the Applications details panel. Selecting
an application, saving workspace choices or opening a review never launches it.
The confirmation shows freshly resolved registration details, including the ID,
desktop file, command entry, working directory, terminal requirement and D-Bus
activation flag. Running state is explicitly unknown: an explicit launch may open
another instance or activate an existing one. Saved duplicate preferences are not
applied by this individual launch action.

That describes unit 4's original manual flow. [Unit 5](custom-applications.md) now
adds a duplicate-policy selector, fresh D-Bus identity checks and reviewed saved
custom tools. Unsupported identities remain unknown; SkipIfRunning blocks them
until the user explicitly reviews with AlwaysLaunch.

## Follow the request

1. `frontend/src/components/ApplicationLaunch.tsx` requests a review by desktop ID.
   It uses the shared native modal and initially focuses Cancel. React sends no
   executable, arguments, file path or approval fingerprint to the launch method.
2. `internal/applications/launch.go` asks its `Launcher` interface for the current
   registration and creates a cryptographically random review token. The backend
   retains the exact reviewed specification for five minutes. At most 64 recent
   reviews are retained; expired records are cleaned up when reviewing again.
3. `internal/platform/linux/desktop_linux.go` resolves through GIO's registered
   list. It preserves the Hidden-override guard from discovery. Review fingerprints
   cover effective metadata plus every byte of the desktop file, including fields
   that the UI does not display. Reads accept regular files only, allow ordinary
   symlinks, and stop at 1 MiB; nonblocking opens reject FIFOs without waiting.
4. Confirm sends only the token. Before dispatch, the adapter resolves again and
   compares fingerprints. A changed, hidden or unavailable registration requires
   a fresh review. The exact GIO object checked in that operation is handed to
   `g_app_info_launch`, with no files or URIs and no shell parsing of `Exec`.
5. The service marks the token in progress before dispatch. Simultaneous calls
   receive that state; subsequent calls return the original outcome. No token can
   dispatch twice. Following an interrupted bridge response, **Check launch result**
   uses the same token. Retry after a terminal failure requires **Review again**.

The GIO call runs on a Go worker, leaving GTK responsive. Cancellation is checked
before dispatch. Once handed to GIO, a late cancellation cannot retract a launch
or be reported as proof that nothing opened. Raw operating-system errors are not
sent to the UI. Expected failures have an actionable message and a code.

## What the result means

**Launch request accepted** means GIO accepted the desktop handoff. It does not
prove a window opened, startup completed, a D-Bus activation succeeded later or a
service became ready. GIO handles desktop quoting, field codes, `Path`, `Terminal`
and activation/fallback behavior. Workspace Manager neither reconstructs the
command nor treats a desktop application's PID as an owned development server.

This unit does not scan process names, skip an allegedly running application,
capture its windows or kill it. Workspace execution, custom argv configuration
and supported instance adapters remain later work. Approval and outcomes are
memory-only in the current GUI process; closing it discards them. They are not a
durable cross-process deduplication guarantee. After a lost response or restart,
check whether the application opened before deliberately requesting another launch.

The WebView click currently supplies no native GDK activation token. GNOME decides
focus and may leave the new window in the background. Installed files/executables
remain controlled by the local user and package manager; fingerprint checks detect
changes between review and dispatch, rather than providing a sandbox against
concurrent replacement of executable code or desktop registrations.

## Verification

From the repository root, with the pinned CLI in PATH:

```bash
wails3 build
go test -race ./...
go vet ./...
npm --prefix frontend test
go build -tags production,mcp -buildvcs=false -o bin/workspace-manager-smoke .
python3 scripts/native-smoke.py
./bin/workspace-manager
```

Go service tests cover review-only behavior, token replay and concurrency, expiry,
bounded capacity, invalid reviews, safe failures and pre-dispatch cancellation.
Isolated GIO subprocess fixtures actually launch a test-owned helper, verifying
arguments with spaces, literal shell punctuation, desktop field-code expansion and
the working directory. They reject post-review edits, Hidden overrides and cancelled
requests, and exercise a launch error. Frontend tests cover cancel, busy controls,
interrupted responses, same-token checks, renewed reviews and stale responses.
The native Wayland script exercises the real bridge/modal and a test-owned helper;
it leaves installed user applications untouched.

Manual follow-up: check a graphical installed application, a D-Bus activatable
application and a Terminal=true application on GNOME; observe focus/background
behavior and early startup failures. Physical resizing, keyboard-only operation,
screen readers and X11 remain manual acceptance checks. The automated helper result
does not establish those behaviors.

## Platform references

- [GIO AppInfo.launch](https://docs.gtk.org/gio/method.AppInfo.launch.html)
  documents the handoff API, inherited environment and startup-success limits.
- [GIO DesktopAppInfo](https://docs.gtk.org/gio-unix/class.DesktopAppInfo.html)
  documents the desktop-entry implementation used for resolution and launch.
