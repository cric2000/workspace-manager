# Custom tools and conservative instance checks

Phase 4, unit 5 adds custom executables to workspace application lists. Open
**Workspaces → Apps → Add custom executable**, enter a name, absolute executable
path and absolute working directory, then add one row per argument. **Keep in
draft** returns to the ordered application list; **Save applications** persists
the whole draft. Cancel discards it. Saving never executes the tool.

Spaces remain within an argument row; an empty row means an empty argument. Do not
add shell quotes around values. There is no implicit shell, PATH lookup, tilde,
environment-variable expansion or desktop field-code expansion for custom tools.
For example, `two words`, `; $HOME` and `%U` are three literal arguments. A custom
tool opens without a terminal; configure a terminal emulator explicitly if needed.

Custom tools are workspace-scoped. They have server-assigned stable IDs, names,
executable paths, literal argv, working directories and an optional well-known
session D-Bus identity. The workspace's duplicate policy applies to each entry.
Deleting its configuration does not delete executable files or stop a running app.
Duplication preserves configuration with independent argument lists. Paths may be
offline or unavailable while editing; review checks them before allowing execution.
Names are limited to 120 characters, paths to 4,096 bytes, and argv to 128 entries,
8,192 characters each and 32 KiB combined, without NUL. Existing document-size
limits still apply. Arguments are stored in the workspace JSON.

## Review and launch

Open **Applications → Custom applications** to see tools from saved workspaces.
Refresh saved tools after editing in another window. Choose a duplicate preference
for this individual action, then **Review launch**. Its initial preference comes
from the workspace; changing the launch preference does not change the saved one.

The bridge submits only workspace ID, revision, custom ID and duplicate policy.
`workspaces.NewCustomResolver` reads the saved configuration and requires the exact
displayed revision. The launch bridge accepts neither custom argv nor executable
paths. `linux.CustomLauncher` checks a regular executable with execute permission
bits, an existing working directory, resolved symlinks and file identity metadata.
Review retains a configuration/metadata fingerprint in the existing bounded,
five-minute single-use token store.

Confirm rechecks the saved revision, executable metadata and running identity.
Configuration edits, executable replacement and symlink retargeting between those
checks require a fresh review. Directory activity does not invalidate approval:
the directory fingerprint covers identity/permissions, not ordinary content changes.
The OS remains authoritative about ACLs, mount flags and executable format at start.
Checks do not sandbox a user concurrently replacing executable code after dispatch.

The adapter uses Go `exec.Command` with an argument slice and explicit working
directory. It starts a separate session, uses `/dev/null` for stdin/stdout/stderr
and reaps the child while the GUI lives. It drops inherited activation/startup
tokens and the parent's GIO desktop identity. A successful start is a handoff,
without readiness, output capture or managed-service ownership. Closing the GUI
does not deliberately terminate an external tool. Use the future controller for
managed project commands, logs, readiness and stop/reconnect behavior.

Custom and desktop launches share safe failure messages, token replay protection
and same-token outcome checks after interrupted responses. Approval/outcomes are
memory-only; check whether a tool opened before deliberately launching again after
a GUI restart or an unavailable outcome.

## What an instance check establishes

`linux.DesktopInstances` calls session-bus `GetNameOwner` on the bus daemon, with
cancellation, a two-second overall budget and a 1.5-second method timeout. It does
not activate the target application or autolaunch a bus. A missing session address
is unknown. Each check uses a private GIO connection, avoiding changes to the GUI's
shared bus lifecycle or an exit-on-close side effect. It uses the existing GIO stack; the existing
pinned Godbus module is used only by private-bus test fixtures.

For desktop entries, identity is derived from the desktop ID only when the entry
declares `DBusActivatable=true` and the resulting well-known bus name is valid.
Legacy/unrecognized/non-D-Bus applications remain unknown. Custom tools may supply
an identity documented by the application, or leave it blank. A custom identity is
an explicit mapping supplied by the user; it does not prove the service was started
with these exact arguments, a browser profile, a project or a particular window.
Unique connection names and the bus daemon's name are not accepted as identities.

| Observation at confirmation | Skip a confirmed running instance | Always request a launch |
| --- | --- | --- |
| Configured identity has an owner | Return `skipped`; do not dispatch | Dispatch the reviewed request |
| Configured identity has no owner | Dispatch the reviewed request | Dispatch the reviewed request |
| No supported identity, check failed or timed out | Return `instance_unknown`; do not dispatch | Dispatch the reviewed request |

An unknown result offers a preference change followed by **Review again**. The user
must choose Always request a launch and confirm a fresh review to proceed. There
is no silent fallback from uncertainty to launch, no executable-name/PID matching
and no operation that signals or kills an existing application.

Ownership is a point-in-time service observation, rechecked at confirmation rather
than reused from review. It can change immediately afterward. Absence does not rule
out another instance that has not acquired the name or uses a different identity.
This is not atomic cross-GUI deduplication or a window/readiness guarantee.

## Workspace schema 3

`internal/storage/workspace_codec.go` reads schemas 1 and 2 through explicit,
strict migrations. Schema 3 allows exactly one desktop ID or custom configuration
per application entry, retaining the existing order and duplicate policy. Desktop
entries retain their prior JSON shape; custom entries use an empty desktop ID and
a `custom` object. Duplicate desktop/custom IDs are rejected within their respective
workspace namespaces. Settings, projects and sessions remain schema 1.

Reads migrate in memory. The next explicit mutation archives the exact previous
document before replacing it with schema 3. Schema-2 migration preserves desktop
choices and refuses custom or unknown fields smuggled into an older envelope.
Older binaries reject the newer schema instead of editing away custom commands.
`domain.CloneApplications` copies both custom records and argv to keep views,
input drafts and duplicated workspaces independent.

## Verification

```bash
export PATH="$PWD/.tools/bin:$PATH"
wails3 build
go test -race ./...
go vet ./...
npm --prefix frontend test
go build -tags production,mcp -buildvcs=false -o bin/workspace-manager-smoke .
python3 scripts/native-smoke.py
./bin/workspace-manager
```

Tests use temporary directories and test-owned helpers. Private-bus integration
tests require `dbus-daemon` and use a private configuration without activation
services, observing no owner → owner → released and an unreachable bus. No user
desktop application is activated by those checks. System decoder-helper/private-bus
tests need their normal local IPC permissions. See [validation.md](validation.md)
for observed native results and remaining manual graphical/terminal/focus checks.

## References

- [Desktop Entry D-Bus activation](https://specifications.freedesktop.org/desktop-entry/latest/dbus.html)
  specifies the desktop-file-to-service-name mapping.
- [D-Bus specification](https://dbus.freedesktop.org/doc/dbus-specification.html)
  specifies ownership and GetNameOwner.
- [Go os/exec](https://pkg.go.dev/os/exec) documents literal argv, Start and Wait.

Phase 4's implementation units now cover discovery, saved desktop/custom choices,
reviewed individual launch and conservative identities. Installed graphical apps,
D-Bus activation, Terminal=true launch/focus behavior and other manual acceptance
gates remain unverified here. Ordered workspace execution belongs to Phase 8.
The next implementation phase is read-only project detection and reviewed startup
configuration; it must not execute project code during detection.
