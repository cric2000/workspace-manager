#!/usr/bin/env bash
set -euo pipefail
task_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
task_version="${1:-1.0.0}"
if [[ ! "$task_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Use a numeric major.minor.patch version." >&2
  exit 1
fi
cd "$task_root"
test -x bin/workspace-manager
test "$(dpkg --print-architecture)" = amd64
task_stage="$(mktemp -d /tmp/workspace-manager-package.XXXXXXXX)"
trap 'rm -rf -- "$task_stage"' EXIT
chmod 755 "$task_stage"
install -Dm755 bin/workspace-manager "$task_stage/usr/bin/workspace-manager"
install -Dm644 packaging/linux/io.github.workspace_manager.WorkspaceManager.desktop "$task_stage/usr/share/applications/io.github.workspace_manager.WorkspaceManager.desktop"
install -Dm644 packaging/linux/io.github.workspace_manager.WorkspaceManager.svg "$task_stage/usr/share/icons/hicolor/scalable/apps/io.github.workspace_manager.WorkspaceManager.svg"
install -Dm644 LICENSE "$task_stage/usr/share/doc/workspace-manager/copyright"
install -Dm644 LICENSE "$task_stage/usr/share/doc/workspace-manager/LICENSE"
install -Dm644 README.md "$task_stage/usr/share/doc/workspace-manager/README.md"
for task_document in docs/architecture.md docs/roadmap.md docs/screenshots/dashboard.png docs/screenshots/activity.png docs/screenshots/sessions.png; do
  install -Dm644 "$task_document" "$task_stage/usr/share/doc/workspace-manager/$task_document"
done
mkdir -p "$task_stage/DEBIAN"
cat > "$task_stage/DEBIAN/control" <<EOF
Package: workspace-manager
Version: $task_version
Section: devel
Priority: optional
Architecture: amd64
Maintainer: Workspace Manager contributors
Depends: libgtk-4-1, libwebkitgtk-6.0-4, libglib2.0-0t64
Description: Local Linux developer workspace manager
 Reviewed workspace launching, independent process supervision, session
 snapshots, login startup and normal GNOME Save and Shutdown.
EOF
desktop-file-validate "$task_stage/usr/share/applications/io.github.workspace_manager.WorkspaceManager.desktop"
dpkg-deb --root-owner-group --build "$task_stage" "bin/workspace-manager_${task_version}_amd64.deb"
echo "Package created. No system files were installed or modified."
