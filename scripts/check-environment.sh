#!/usr/bin/env bash
# Read-only prerequisite checks; this script never installs system packages.
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  echo 'V1 targets Linux AMD64.' >&2
  exit 1
fi

for executable in go node npm gcc pkg-config; do
  if ! command -v "$executable" >/dev/null; then
    echo "Missing prerequisite: $executable" >&2
    exit 1
  fi
done

go version
node --version
npm --version
node -e 'if (Number(process.versions.node.split(".")[0]) < 24) { console.error("Use Node 24 LTS or later."); process.exit(1) }'
go_version="$(go env GOVERSION)"
if [[ ! "$go_version" =~ ^go1\.([0-9]+)\. || "${BASH_REMATCH[1]:-0}" -lt 26 ]]; then
  echo 'Wails v3.0.0 requires Go 1.26 or later.' >&2
  exit 1
fi

printf 'GTK4: '
pkg-config --modversion gtk4
printf 'WebKitGTK 6.0: '
pkg-config --modversion webkitgtk-6.0
printf 'GIO Unix: '
pkg-config --modversion gio-unix-2.0
printf 'GdkPixbuf: '
pkg-config --modversion gdk-pixbuf-2.0

wails_cli="$project_root/.tools/bin/wails3"
if [[ ! -x "$wails_cli" ]]; then
  wails_cli="$(command -v wails3 || true)"
fi
if [[ -z "$wails_cli" || "$("$wails_cli" version 2>&1)" != v3.0.0 ]]; then
  echo 'Install the pinned Wails v3.0.0 CLI; see README.md.' >&2
  exit 1
fi
"$wails_cli" version

case "${XDG_SESSION_TYPE:-}" in
  wayland|x11) printf 'Desktop session: %s\n' "$XDG_SESSION_TYPE" ;;
  *) echo 'Desktop session: unknown (native GUI requires an active desktop session)' ;;
esac
echo 'Prerequisite checks passed. Build and native window checks are separate.'
