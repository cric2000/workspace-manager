# Workspace Manager

A local Linux developer workspace manager built with Go, Wails v3, React and TypeScript.

![Workspaces home](docs/screenshots/dashboard.png)

[Activity](docs/screenshots/activity.png) · [Saved sessions](docs/screenshots/sessions.png)
— captured from an isolated native test workspace.

## Features

- Create workspaces and Go, Node/TypeScript, Next.js, Python or FastAPI projects.
- Import projects; review literal commands, editors, environment references and previews.
- Launch ordered applications/projects with delays, readiness checks and duplicate prevention.
- Keep managed servers running after the GUI closes; reconnect to bounded logs and stop controls.
- Save, restore, duplicate and update sessions with recoverable version history.
- Choose a workspace, a saved session or both to open after login.
- Save and verify a snapshot before gracefully stopping managed services and opening normal GNOME shutdown.

V1 implementation is available. Real logout/login, power-off, X11, assistive-technology
and clean-system installation acceptance checks remain pending; see [roadmap](docs/roadmap.md).
Snapshots preserve supported launch metadata, not source files, unsaved documents,
arbitrary browser tabs or terminal history. Project execution always requires approval.

## Build and run

Ubuntu 26.04 GNOME, Linux AMD64; Go 1.26+, Node.js 24+ and npm.

```bash
sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev desktop-file-utils
mkdir -p .tools/bin
GOBIN="$PWD/.tools/bin" go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
export PATH="$PWD/.tools/bin:$PATH"
npm --prefix frontend ci
wails3 build
./bin/workspace-manager
```

Use `wails3 dev` for development. Configuration and state follow XDG directories.
Environment files accept literal `KEY=VALUE` lines, with optional enclosing quotes;
there is no shell expansion. Output redaction is best effort; avoid printing secrets.

## Test and package

```bash
export PATH="$PWD/.tools/bin:$PATH"
wails3 task test
wails3 task package
```

The package is `bin/workspace-manager_1.0.0_amd64.deb`. Packaging never installs it.

```bash
sudo apt install ./bin/workspace-manager_1.0.0_amd64.deb
# Disable login startup and stop managed workers in Activity before uninstalling.
sudo apt remove workspace-manager
```

Uninstall preserves configuration, snapshots and project directories. The GUI closes
independently of managed workers. `workspace-manager --status` prints controller
state; `--stop-controller` disconnects the controller while preserving workers.

## Daily workflow

The three main sections are **Workspaces**, **Projects** and **Saved sessions**.
A workspace is a reusable setup; a saved session captures what is open at one point in time.

1. In Workspaces, create a workspace and add apps/projects. Review project startup
   commands, then click **Launch**. **More** contains launch preferences and other occasional actions.
2. Use Projects to create, import or launch individual projects. Activity shows running
   services, output and stop controls.
3. Use **Save session** in the top bar to capture your current setup. In Saved sessions,
   click **Restore** to resume it or **Duplicate** to make a copy; **More** contains updates and version history.
4. Open Settings to choose what starts after login. App library and Diagnostics are under **Tools & help**.
5. Use **Save & Shutdown** to review and save your session before shutdown. It is available
   in the top bar and on Saved sessions. `Ctrl+K` opens searchable quick actions from any screen.

Native fixture checks: build with `go build -tags production,mcp -o bin/workspace-manager-smoke .`,
then run `python3 scripts/native-smoke.py --lifecycle-only` in a graphical session.
Use `--login-only` to check all startup modes and desktop session restore.
These use owned fixtures and never power off. Production builds have no MCP endpoint.
Add `--screenshots` to refresh the native screenshots. Run `python3 scripts/check-package.py`
after packaging to verify isolated unpack/removal and the production CLI.

[Architecture](docs/architecture.md) · [MIT license](LICENSE)
