"""Validate installation/removal in a temporary dpkg database, never the system."""

import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
PACKAGE = ROOT / 'bin/workspace-manager_1.0.0_amd64.deb'


def main():
    with tempfile.TemporaryDirectory(prefix='workspace-package-check-') as directory:
        temporary = Path(directory)
        installed = temporary / 'root'
        admin = temporary / 'dpkg'
        installed.mkdir()
        for child in ['info', 'updates', 'triggers']:
            (admin / child).mkdir(parents=True)
        (admin / 'status').write_text('')
        (admin / 'available').write_text('')
        command = ['dpkg', '--log=' + str(admin / 'dpkg.log'), '--force-not-root', '--force-bad-path', '--admindir=' + str(admin), '--instdir=' + str(installed)]
        subprocess.run(command + ['--unpack', str(PACKAGE)], check=True)
        icon = installed / 'usr/share/icons/hicolor/scalable/apps/io.github.workspace_manager.WorkspaceManager.svg'
        assert icon.read_bytes() == (ROOT / 'frontend/src/assets/app-icon.svg').read_bytes()
        binary = installed / 'usr/bin/workspace-manager'
        assert binary.read_bytes() == (ROOT / 'bin/workspace-manager').read_bytes()
        subprocess.run(['desktop-file-validate', str(installed / 'usr/share/applications/io.github.workspace_manager.WorkspaceManager.desktop')], check=True)
        environment = dict(os.environ, XDG_CONFIG_HOME=str(temporary / 'config'), XDG_STATE_HOME=str(temporary / 'state'), XDG_CACHE_HOME=str(temporary / 'cache'))
        source = temporary / 'user-project'
        source.mkdir()
        (source / 'main.py').write_text('# user-owned source; never executed\n')
        try:
            result = subprocess.run([str(binary), '--status'], env=environment, check=True, capture_output=True, text=True, timeout=20)
            state = json.loads(result.stdout)
            assert not state['code'] and state['runtime']['runs'] == []
        finally:
            subprocess.run([str(binary), '--stop-controller'], env=environment, check=True, timeout=20)
        settings = temporary / 'config/workspace-manager/settings.json'
        before = settings.read_bytes()
        subprocess.run(command + ['--remove', 'workspace-manager'], check=True)
        assert not binary.exists()
        assert settings.read_bytes() == before
        assert (source / 'main.py').read_text() == '# user-owned source; never executed\n'
    print('PASS: isolated dpkg unpack/removal, package payload equality, desktop entry, production CLI/controller, retained settings and untouched project source; system installation unchanged.')


if __name__ == '__main__':
    main()
