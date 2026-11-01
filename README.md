# Workspace Manager

A local Linux developer workspace manager built with Go, Wails v3, React,
TypeScript and Tailwind CSS.

## Status

In development. Workspace management, reviewed application launching, and project
inspection/configuration are available. Managed project execution, session restore,
login startup and Save & Shutdown are still being built.

## Features

- Create, edit, duplicate and delete workspaces; choose a default and order projects.
- Discover desktop applications and review desktop/custom executable launches.
- Inspect projects, compare rescans and save reviewed commands, environment-file references,
  readiness, preview, editor, stop and delay settings. Saving runs no project code.
- Remove unused project registrations while preserving source files and history.
- Store configuration locally using XDG paths, versioned JSON and atomic writes.

## Requirements

- Ubuntu 26.04, GNOME Wayland, Linux AMD64.
- Go 1.26+, Node.js 24+ and npm.
- Wails CLI v3.0.0 and GTK4/WebKitGTK 6.0 development libraries.

```bash
sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev
```

## Build and run

```bash
mkdir -p .tools/bin
GOBIN="$PWD/.tools/bin" go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
export PATH="$PWD/.tools/bin:$PATH"
npm --prefix frontend ci
wails3 build
./bin/workspace-manager
```

The build regenerates frontend bindings and embeds the frontend in the executable.
For development with hot reload, run `wails3 dev` after setup.

## Test

```bash
export PATH="$PWD/.tools/bin:$PATH"
wails3 task test
```

This builds the frontend and runs Go race tests, Go vet and frontend tests.
Native Wayland checks are available in `scripts/native-smoke.py`.

See the [roadmap](docs/roadmap.md) for remaining work and
[architecture](docs/architecture.md) for design decisions.
