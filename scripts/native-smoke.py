"""Native Wayland profiles, applications, project inspection and registration checks.

Build with: go build -tags production,mcp -buildvcs=false -o bin/workspace-manager-smoke .
Run from a graphical Wayland session: python3 scripts/native-smoke.py
For project-only iteration: python3 scripts/native-smoke.py --projects-only
All app data and project fixtures are temporary. The normal production binary has
no MCP endpoint. No project commands run here; application tests use owned helpers.
"""

import argparse
import http.client
import json
import os
from pathlib import Path
import secrets
import shlex
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request


ROOT = Path(__file__).resolve().parents[1]


class Native:
    def __init__(self, temporary):
        self.token = secrets.token_urlsafe(32)
        self.counter = 0
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            self.port = probe.getsockname()[1]
        self.endpoint = f"http://127.0.0.1:{self.port}/mcp"
        env = dict(os.environ, WAILS_MCP_HOST="127.0.0.1", WAILS_MCP_PORT=str(self.port),
                   WAILS_MCP_TOKEN=self.token, WAILS_MCP_TIMEOUT="5000", WAILS_MCP_HIDE_CURSOR="1",
                   GDK_BACKEND="wayland", XDG_CONFIG_HOME=str(temporary / "config"),
                   XDG_STATE_HOME=str(temporary / "state"), XDG_CACHE_HOME=str(temporary / "cache"),
                   XDG_DATA_HOME=str(temporary / "data"))
        self.log_path = temporary / "native.log"
        self.log = self.log_path.open("w")
        self.process = subprocess.Popen([str(ROOT / "bin/workspace-manager-smoke")], cwd=ROOT,
                                        env=env, stdout=self.log, stderr=self.log, start_new_session=True)

    def __enter__(self):
        try:
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                try:
                    with urllib.request.urlopen(f"http://127.0.0.1:{self.port}/", timeout=1) as response:
                        assert json.load(response)["service"] == "wails-mcp"
                    break
                except (OSError, ValueError):
                    if self.process.poll() is not None:
                        raise RuntimeError("Native startup failed: " + self.log_path.read_text())
                    time.sleep(0.1)
            else:
                raise RuntimeError("No native test endpoint: " + self.log_path.read_text())
            self.rpc("initialize", dict(protocolVersion="2025-06-18", capabilities={},
                                        clientInfo={"name": "workspace-smoke", "version": "1"}))
            self.until("document.querySelector('[data-new-workspace]')?.disabled === false")
            return self
        except BaseException:
            self.__exit__(None, None, None)
            raise

    def __exit__(self, *_):
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        self.log.close()

    def rpc(self, method, params):
        self.counter += 1
        payload = json.dumps(dict(jsonrpc="2.0", id=self.counter, method=method, params=params)).encode()
        request = urllib.request.Request(self.endpoint, data=payload,
                                         headers={"Content-Type": "application/json", "Authorization": "Bearer " + self.token})
        with urllib.request.urlopen(request, timeout=8) as response:
            result = json.load(response)
        if "error" in result:
            raise RuntimeError(str(result["error"]))
        return result["result"]

    def tool(self, name, **arguments):
        result = self.rpc("tools/call", dict(name=name, arguments=arguments))
        if result.get("isError"):
            raise RuntimeError(str(result))
        text = result["content"][0]["text"]
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            return text

    def js(self, source):
        return self.tool("js_eval", js=source)

    def until(self, expression):
        deadline = time.monotonic() + 12
        while time.monotonic() < deadline:
            if self.js("return Boolean(" + expression + ");"):
                return
            time.sleep(0.1)
        raise AssertionError(expression + "\n" + str(self.js("return {width:innerWidth, height:innerHeight, text:document.body.innerText};")))

    def click(self, selector):
        self.tool("mouse_click", selector=selector)

    def fill(self, selector, text):
        # Set a complete controlled-field value atomically. Selection plus a
        # later RPC can lose the selection to native focus changes. Real OS key
        # behavior remains manual; frontend tests exercise ordinary typing.
        self.js("const field = document.querySelector(" + json.dumps(selector) + "); field.focus(); const prototype = field instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype; Object.getOwnPropertyDescriptor(prototype, 'value').set.call(field, " + json.dumps(text) + "); field.dispatchEvent(new InputEvent('input', {bubbles:true, inputType:'insertText'})); return true;")
        self.until("document.querySelector(" + json.dumps(selector) + ").value === " + json.dumps(text))

    def narrow_viewport(self):
        # Configure events and WebKit viewport updates can arrive late. Do this
        # only after interactive checks; wait for a stable CSS viewport before
        # inspecting layout. Native zoom also allows a compositor-independent
        # CSS check when a physical resize request is declined.
        self.tool("window_control", action="focus")
        self.tool("window_control", action="unmaximise")
        self.tool("window_control", action="set_size", width=760, height=600)
        self.tool("window_control", action="set_zoom", zoom=1.01)
        zoom, stable = 1.01, 0
        for _ in range(30):
            time.sleep(0.25)
            width = self.js("return innerWidth;")
            if 740 <= width <= 800:
                stable += 1
                if stable < 4:
                    continue
                state = self.tool("window_control", action="focus")
                if state["width"] > 800 or abs(zoom - 1) > 0.02:
                    print("NOTE: narrow CSS viewport used native WebView zoom; physical window resizing remains manual.")
                return
            stable = 0
            zoom *= width / 760
            self.tool("window_control", action="set_zoom", zoom=zoom)
        raise AssertionError("Narrow native CSS viewport did not settle")

    def submit(self):
        self.click('dialog button[type="submit"]')
        self.until("!document.querySelector('dialog')")

    def assert_layout(self):
        metrics = self.js("return {width:innerWidth, scroll:document.documentElement.scrollWidth, dialog:document.querySelector('dialog') && {client:document.querySelector('dialog').clientWidth, scroll:document.querySelector('dialog').scrollWidth}, overflow:[...document.querySelectorAll('body *')].map(e=>({tag:e.tagName, classes:e.className, right:e.getBoundingClientRect().right})).filter(e=>e.right>innerWidth+1).slice(0,12)};")
        # Fractional native zoom can round scrollWidth up and innerWidth down.
        # Permit their single CSS-pixel rounding difference, not content overflow.
        assert metrics['scroll'] <= metrics['width'] + 1, metrics
        if metrics['dialog']:
            assert metrics['dialog']['scroll'] <= metrics['dialog']['client'] + 1, metrics

    def close(self):
        try:
            self.tool("window_control", action="close")
        except (http.client.RemoteDisconnected, ConnectionResetError):
            pass  # The final window can close its endpoint before replying.
        assert self.process.wait(timeout=8) == 0


def main():
    sys.stdout.reconfigure(line_buffering=True)
    parser = argparse.ArgumentParser()
    parser.add_argument("--projects-only", action="store_true", help="Run the isolated project inspection/settings workflow")
    arguments = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="workspace-native-check-") as directory:
        temporary = Path(directory)
        if arguments.projects_only:
            check_projects(temporary)
            return
        applications = temporary / "data/applications"
        applications.mkdir(parents=True)
        icon = temporary / "fixture-icon.svg"
        icon.write_text('<svg xmlns="http://www.w3.org/2000/svg" width="128" height="128"><rect width="128" height="128" rx="20" fill="#286c50"/></svg>')
        editor_id = "org.example.WorkspaceSmokeEditor.desktop"
        editor_file = applications / editor_id
        launch_capture = temporary / "launch-capture"
        launch_helper = temporary / "launch helper"
        launch_helper.write_text('#!/bin/sh\nprintf \'%s\\n\' "$@" >> ' + shlex.quote(str(launch_capture)) + '\n')
        launch_helper.chmod(0o700)
        launch_entry = '"' + str(launch_helper) + '" "two words" %U'
        editor_file.write_text('[Desktop Entry]\nType=Application\nName=Workspace Smoke Editor\nExec=' + launch_entry + '\nIcon=' + str(icon) + '\n')
        (applications / "org.example.WorkspaceSmokeTerminal.desktop").write_text('[Desktop Entry]\nType=Application\nName=Workspace Smoke Terminal\nExec=/bin/true\nIcon=utilities-terminal\n')
        with Native(temporary) as app:
            app.until("document.body.innerText.includes('Start with a space for your work')")
            app.click("[data-new-workspace]")
            app.until("document.querySelector('dialog')?.matches(':modal')")
            assert app.js("return document.activeElement.id;") == "workspace-name"
            # Real WebKit modality makes the rest of the page inert.
            assert app.js("document.querySelector('nav button').focus(); return document.activeElement.closest('dialog') !== null;")
            app.fill("#workspace-name", "Coding")
            app.fill("#workspace-description", "Native profile smoke check")
            app.submit()
            app.until("document.querySelector('article[aria-label=\"Coding\"]')")
            app.click('button[aria-label="Edit Coding"]')
            app.until("document.querySelector('#workspace-name')")
            app.fill("#workspace-name", "Backend")
            app.submit()
            app.until("document.querySelector('article[aria-label=\"Backend\"]')")
            app.click('button[aria-label="Configure applications for Backend"]')
            app.until("document.querySelector('dialog')?.matches(':modal')")
            assert app.js("return document.activeElement.getAttribute('aria-label');") == 'Find applications to add'
            app.fill('input[aria-label="Find applications to add"]', 'Workspace Smoke')
            app.until("document.querySelector('.picker-available button[aria-label*=\"Workspace Smoke Editor\"]')")
            app.click('.picker-available button[aria-label="Add Workspace Smoke Editor (' + editor_id + ')"]')
            app.click('.picker-available button[aria-label="Add Workspace Smoke Terminal (org.example.WorkspaceSmokeTerminal.desktop)"]')
            app.click('button[aria-label="Move Workspace Smoke Terminal up"]')
            app.js("const select = document.querySelector('select[aria-label=\"Duplicate preference for Workspace Smoke Terminal\"]'); select.value = 'always_launch'; select.dispatchEvent(new Event('change', {bubbles:true})); return true;")
            app.click('dialog .dialog-actions .primary-button')
            app.until("!document.querySelector('dialog')")
            app.until("document.querySelector('article[aria-label=\"Backend\"]').textContent.includes('Apps (2)')")
            app.click('button[aria-label="Duplicate Backend"]')
            app.until("document.querySelector('#workspace-name')")
            app.fill("#workspace-name", "Learning")
            app.submit()
            app.until("document.querySelectorAll('article').length === 2")
            app.click('button[aria-label="Set Backend as default"]')
            app.until("document.querySelector('.default-name').textContent === 'Backend'")
            app.click('button[aria-label="Delete Backend"]')
            app.until("document.querySelector('dialog')")
            # A synthetic cancel event verifies our handler; OS Escape remains manual.
            app.js("document.querySelector('dialog').dispatchEvent(new Event('cancel', {cancelable:true})); return true;")
            app.until("!document.querySelector('dialog')")
            assert app.js("return document.activeElement.getAttribute('aria-label');") == "Delete Backend"
            app.click('button[aria-label="Delete Backend"]')
            app.until("document.querySelector('dialog')")
            app.click('dialog button[type="submit"]')
            app.until("document.querySelector('dialog [role=alert]')")
            assert "clear the default" in app.js("return document.querySelector('dialog [role=alert]').textContent;")
            app.click('dialog button[aria-label="Close dialog"]')
            app.until("!document.querySelector('dialog')")
            old_theme = app.js("return document.documentElement.dataset.theme;")
            app.click('button[aria-label*="theme"]')
            app.until("document.documentElement.dataset.theme !== " + json.dumps(old_theme))
            app.click('nav button[data-page="applications"]')
            app.until("document.querySelector('.application-list')")
            app.fill('input[aria-label="Search installed applications"]', 'Workspace Smoke')
            app.until("document.querySelectorAll('.application-choice').length === 2")
            app.until("document.querySelectorAll('.application-choice img').length === 2 && [...document.querySelectorAll('.application-choice img')].every(image => image.complete && image.naturalWidth > 0)")
            app.click('button[aria-label="Inspect Workspace Smoke Editor (' + editor_id + ')"]')
            app.until("document.querySelector('.application-details').textContent.includes('" + editor_id + "')")
            assert app.js("return document.querySelector('.launch-entry').textContent;") == launch_entry
            assert app.js("return document.documentElement.scrollWidth <= innerWidth;")
            app.click('.application-details .primary-button')
            app.until("document.querySelector('dialog .launch-review')")
            assert app.js("return document.activeElement.textContent;") == 'Cancel'
            assert 'Running state is unknown' in app.js("return document.querySelector('dialog').textContent;")
            app.click('dialog .dialog-actions .secondary-button')
            assert not launch_capture.exists()
            app.click('.application-details .primary-button')
            app.until("document.querySelector('dialog .launch-review')")
            app.click('dialog .dialog-actions .primary-button')
            app.until("document.querySelector('dialog [role=status]')?.textContent.includes('Launch request accepted')")
            for _ in range(60):
                if launch_capture.exists() and launch_capture.read_text() == 'two words\n':
                    break
                time.sleep(0.05)
            else:
                raise AssertionError('GIO did not run the reviewed test-owned helper with the expected arguments')
            app.click('dialog .dialog-actions .secondary-button')
            app.click('.application-details .primary-button')
            app.until("document.querySelector('dialog .launch-review')")
            with editor_file.open('a') as registration:
                registration.write('\nX-Test-Changed-After-Review=true\n')
            app.click('dialog .dialog-actions .primary-button')
            app.until("document.querySelector('dialog [role=status]')?.textContent.includes('changed after review')")
            assert launch_capture.read_text() == 'two words\n'
            app.click('dialog .dialog-actions .secondary-button')
            print('PASS: native launch review/cancel, GIO handoff to a test-owned helper, exact quoted argument and changed-registration rejection.')

            def refresh_until(expression):
                # GIO directory-monitor invalidation is asynchronous. Exercise
                # the real refresh button until that event has reached GTK.
                for _ in range(20):
                    app.click('.workspace-heading .secondary-button')
                    app.until("!document.querySelector('.workspace-heading .secondary-button').disabled")
                    if app.js("return Boolean(" + expression + ");"):
                        return
                    time.sleep(0.15)
                raise AssertionError("Native catalogue did not refresh: " + expression)

            added = applications / "org.example.WorkspaceSmokeAdded.desktop"
            added.write_text('[Desktop Entry]\nType=Application\nName=Workspace Smoke Added\nExec=/bin/true\n')
            refresh_until("document.querySelectorAll('.application-choice').length === 3")
            editor_file.unlink()  # Only this test-owned desktop registration.
            refresh_until("document.querySelectorAll('.application-choice').length === 2 && !document.querySelector('.application-details').textContent.includes('" + editor_id + "')")
            print("PASS: native catalogue search, selection, local/themed PNG icons and live add/remove refresh; only the test-owned helper was launched.")

            app.click('nav button[data-page="diagnostics"]')
            app.until("document.body.innerText.includes('Desktop bridge connected')")
            assert "wayland" in app.js("return document.body.innerText;")
            app.click(".environment-panel .secondary-button")
            app.until("document.body.innerText.includes('Desktop bridge connected')")
            app.click('nav button[data-page="workspaces"]')
            app.until("document.querySelector('article[aria-label=\"Backend\"]')")
            app.click('button[aria-label="Configure applications for Backend"]')
            app.until("document.querySelector('dialog')?.matches(':modal')")
            app.narrow_viewport()
            app.assert_layout()
            app.close()
            print("PASS: native Wayland CRUD/default protection, modal focus/inertness, cancel handler, theme, narrow viewport layout and diagnostics.")

        profiles = temporary / "config/workspace-manager/profiles"
        records = {json.loads(path.read_text())["data"]["name"]: json.loads(path.read_text())["data"]
                   for path in profiles.glob("*.json")}
        assert set(records) == {"Backend", "Learning"}
        assert records["Backend"]["id"] != records["Learning"]["id"]
        assert records["Learning"]["description"] == records["Backend"]["description"]
        expected_applications = [
            {"desktopId": "org.example.WorkspaceSmokeTerminal.desktop", "duplicatePolicy": "always_launch"},
            {"desktopId": editor_id, "duplicatePolicy": "skip_if_running"},
        ]
        assert records["Backend"]["applications"] == expected_applications
        assert records["Learning"]["applications"] == expected_applications
        assert all(json.loads(path.read_text())["schemaVersion"] == 3 for path in profiles.glob("*.json"))
        settings = json.loads((temporary / "config/workspace-manager/settings.json").read_text())["data"]
        assert settings["defaultWorkspaceId"] == records["Backend"]["id"]
        assert not settings["autostart"]

        with Native(temporary) as app:
            app.until("document.querySelectorAll('article').length === 2")
            assert app.js("return document.querySelector('.default-name').textContent;") == "Backend"
            app.click('button[aria-label="Configure applications for Backend"]')
            app.until("document.querySelector('dialog')?.textContent.includes('Unavailable · kept in this workspace')")
            assert app.js("return [...document.querySelectorAll('.selected-applications select')].map(select => select.value);") == ['always_launch', 'skip_if_running']
            app.click('dialog .dialog-actions .primary-button')
            app.until("!document.querySelector('dialog')")
            app.click('button[aria-label="Configure applications for Learning"]')
            app.until("document.querySelector('dialog')?.textContent.includes('Unavailable · kept in this workspace')")
            app.click('button[aria-label="Remove ' + editor_id + '"]')
            app.click('dialog .dialog-actions .primary-button')
            app.until("!document.querySelector('dialog')")
            assert app.js("return document.querySelector('article[aria-label=\"Learning\"]').textContent.includes('Apps (1)') && document.querySelector('article[aria-label=\"Backend\"]').textContent.includes('Apps (2)');")
            app.click('button[aria-label="Delete Learning"]')
            app.until("document.querySelector('dialog')")
            app.submit()
            app.until("document.querySelectorAll('article').length === 1")
            assert app.js("return document.activeElement.hasAttribute('data-new-workspace');")
            app.click('button[aria-label="Configure applications for Backend"]')
            app.until("document.querySelector('dialog')")
            app.click('button[aria-label="Refresh installed applications"]')
            app.until("!document.querySelector('button[aria-label=\"Refresh installed applications\"]').disabled")
            app.click('dialog section[aria-label="Add installed applications"] > .secondary-button')
            app.until("document.querySelector('#custom-name')")
            app.fill('#custom-name', 'Workspace Custom Fixture')
            app.fill('#custom-executable', str(launch_helper))
            app.fill('#custom-directory', str(temporary))
            app.click('.custom-application-form fieldset > button')
            app.fill('.argument-row input', 'literal ; $HOME %U')
            app.click('.custom-application-form fieldset > button')  # Keep an empty argument.
            app.click('.custom-application-form button[type="submit"]')
            app.until("!document.querySelector('.custom-application-form')")
            app.click('dialog > .dialog-actions .primary-button')
            app.until("!document.querySelector('dialog')")
            assert launch_capture.read_text() == 'two words\n'
            app.click('nav button[data-page="applications"]')
            app.until("document.querySelector('.custom-application-grid article')")
            app.click('.custom-application-grid article .primary-button')
            app.until("document.querySelector('dialog .launch-review')")
            app.click('dialog .dialog-actions .primary-button')
            app.until("document.querySelector('dialog [role=status]')?.textContent.includes('No launch was requested')")
            assert launch_capture.read_text() == 'two words\n'
            app.js("const select = document.querySelector('select[aria-label=\"Review duplicate preference\"]'); select.value = 'always_launch'; select.dispatchEvent(new Event('change', {bubbles:true})); return true;")
            app.click('dialog .dialog-actions .primary-button')
            app.until("document.querySelector('dialog .launch-review') && !document.querySelector('dialog [role=status]')")
            app.click('dialog .dialog-actions .primary-button')
            app.until("document.querySelector('dialog [role=status]')?.textContent.includes('Launch request accepted')")
            for _ in range(60):
                if launch_capture.read_text() == 'two words\nliteral ; $HOME %U\n\n':
                    break
                time.sleep(0.05)
            else:
                raise AssertionError('Custom launch did not preserve literal and empty arguments')
            app.click('dialog .dialog-actions .secondary-button')
            app.close()
        assert len(list(profiles.glob("*.json"))) == 1
        assert (profiles / (records["Backend"]["id"] + ".json")).exists()
        configured = json.loads((profiles / (records["Backend"]["id"] + ".json")).read_text())["data"]["applications"]
        assert configured[:2] == expected_applications
        assert configured[2]['custom']['arguments'] == ['literal ; $HOME %U', '']
        assert configured[2]['custom']['executable'] == str(launch_helper)
        assert len(configured[2]['custom']['id']) == 32
        with Native(temporary) as app:
            app.click('button[aria-label="Configure applications for Backend"]')
            app.until("document.querySelector('dialog')?.textContent.includes('Workspace Custom Fixture')")
            app.click('button[aria-label="Edit custom application Workspace Custom Fixture"]')
            assert app.js("return [...document.querySelectorAll('.argument-row input')].map(input => input.value);") == ['literal ; $HOME %U', '']
            app.narrow_viewport()
            app.assert_layout()
            app.close()
        print('PASS: custom executable configuration persists/reopens, unknown-instance skip blocks dispatch, explicit review launches with literal and empty argv.')
        print("PASS: application order/preferences persist, duplicate independently and retain unavailable references through save/reopen; picker fits a narrow CSS viewport.")
        print("PASS: close/reopen preserves profiles, stable identities and default; confirmed deletion removes only selected metadata and restores focus.")
        check_projects(temporary)


def check_projects(temporary):
    project = temporary / 'inspection-project'
    (project / 'cmd/api').mkdir(parents=True)
    manifest = project / 'package.json'
    manifest.write_text(json.dumps({'packageManager': 'pnpm@9.0.0', 'scripts': {'dev': 'touch must-not-run'}, 'dependencies': {'next': '1', 'typescript': '1'}}))
    (project / 'pnpm-lock.yaml').write_text('lockfileVersion: 9\n')
    (project / 'go.mod').write_text('module example.test/inspection\n\ngo 1.26\n')
    (project / 'cmd/api/main.go').write_text('package main\nfunc main() {}\n')
    (project / '.env').write_text('PRIVATE=never-display-this-inspection-secret\n')
    expected_files = {path.relative_to(project): path.read_bytes() for path in project.rglob('*') if path.is_file()}
    project_tool = temporary / 'project-review-tool'
    project_tool.write_text('#!/bin/sh\ntouch ' + shlex.quote(str(project / 'must-not-run')) + '\n')
    project_tool.chmod(0o700)
    project_records = temporary / 'config/workspace-manager/projects'
    with Native(temporary) as app:
        app.click('nav button[data-page="projects"]')
        app.fill('#project-directory', str(project))
        app.click('.project-path-form .primary-button')
        app.until("document.querySelector('section[aria-label=\"Project inspection results\"]')")
        assert app.js("return document.body.innerText.includes('Next.js') && document.body.innerText.includes('TypeScript');")
        assert not app.js("return document.body.innerText.includes('never-display-this-inspection-secret');")
        app.click('button[aria-label="Inspect candidate pnpm: dev"]')
        assert app.js("return [...document.querySelectorAll('section[aria-label=\"Candidate details\"] li')].map(item => item.textContent);") == ['"run"', '"dev"']
        app.click('button[aria-label="Inspect candidate Go: ./cmd/api"]')
        app.until("document.querySelector('section[aria-label=\"Candidate details\"]').textContent.includes('./cmd/api')")
        manifest.write_text(json.dumps({'packageManager': 'pnpm@9.0.0', 'scripts': {'start': 'touch must-not-run'}, 'dependencies': {'next': '1', 'typescript': '1'}}))
        expected_files[Path('package.json')] = manifest.read_bytes()
        assert app.js("return document.querySelector('button[aria-label=\"Inspect candidate pnpm: dev\"]') !== null;")
        app.click('.project-path-form .primary-button')
        app.until("document.querySelector('button[aria-label=\"Inspect candidate pnpm: start\"]')")
        assert not app.js("return document.querySelector('button[aria-label=\"Inspect candidate pnpm: dev\"]') !== null;")
        app.fill('#project-directory', str(manifest))
        app.click('.project-path-form .primary-button')
        app.until("document.querySelector('[role=alert]')?.textContent.includes('Could not open')")
        app.fill('#project-directory', str(project))
        app.click('.project-path-form .primary-button')
        app.until("document.querySelector('section[aria-label=\"Project inspection results\"]')")
        assert not list(project_records.glob('*.json'))
        app.click('button[aria-label="Inspect candidate Go: ./cmd/api"]')
        app.until("!document.querySelector('.project-import-action .primary-button').disabled")
        app.click('.project-import-action .primary-button')
        app.until("document.querySelector('#registration-name')")
        app.fill('#registration-name', 'Native API')
        app.click('dialog button[type="submit"]')
        app.until("document.querySelector('[aria-label=\"Registration review\"]')")
        assert not list(project_records.glob('*.json'))
        assert app.js("return document.querySelector('dialog .project-command-editor > .launch-review').textContent.includes('/go');")
        app.click('dialog .dialog-actions .secondary-button')
        app.until("document.querySelector('#registration-name')")
        app.fill('.project-command-editor input[placeholder="go or /absolute/path/to/tool"]', str(project_tool))
        app.fill('.project-command-editor .argument-row:nth-of-type(1) input', 'literal ; $HOME %U')
        app.fill('.project-command-editor .argument-row:nth-of-type(2) input', '')
        app.fill('.project-preferences input[type="url"]', 'http://localhost:8123/docs')
        app.click('.project-preferences .project-checkbox input')
        app.until('document.querySelector(".project-preferences input[data-action-field=\\"executable\\"]")')
        app.fill('.project-preferences input[data-action-field="executable"]', str(project_tool))
        app.click('.project-runtime-settings summary')
        app.fill('.project-runtime-settings input[name="launch-delay"]', '3')
        app.click('.project-runtime-settings > fieldset:first-of-type > button')
        app.until('document.querySelector(".project-runtime-settings > fieldset:first-of-type .argument-row input")')
        app.fill('.project-runtime-settings > fieldset:first-of-type .argument-row input', '.env')
        app.js("const field = document.querySelector('select[name=\"readiness-kind\"]'); field.value = 'http'; field.dispatchEvent(new Event('change', {bubbles:true})); return true;")
        app.until('document.querySelector(".project-runtime-settings input[type=url]")')
        app.fill('.project-runtime-settings input[type="url"]', 'http://127.0.0.1:8123/health')
        app.js("const field = document.querySelector('select[name=\"stop-signal\"]'); field.value = 'SIGINT'; field.dispatchEvent(new Event('change', {bubbles:true})); return true;")
        app.until('document.querySelector(".project-runtime-settings .project-checkbox input")')
        app.click('.project-runtime-settings .project-checkbox input')
        app.until('document.querySelector(".project-runtime-settings input[data-action-field=\\"executable\\"]")')
        app.fill('.project-runtime-settings input[data-action-field="executable"]', str(project_tool))
        app.click('dialog button[type="submit"]')
        app.until("document.querySelector('[aria-label=\"Registration review\"]')")
        assert app.js("return [...document.querySelectorAll('dialog .project-command-editor > .launch-review li')].map(item => item.textContent);") == ['"literal ; $HOME %U"', '""']
        assert app.js("return document.querySelector('dialog').textContent.includes('HTTP 200: http://127.0.0.1:8123/health') && document.querySelector('dialog').textContent.includes('SIGINT; 10s grace') && document.querySelector('dialog').textContent.includes('http://localhost:8123/docs');")
        assert not app.js("return document.querySelector('dialog').textContent.includes('never-display-this-inspection-secret');")
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog') && document.querySelector('article[aria-label=\"Registered project Native API\"]')")
        app.narrow_viewport()
        app.assert_layout()
        app.close()
    assert not (project / 'must-not-run').exists()
    records = list(project_records.glob('*.json'))
    assert len(records) == 1
    registered = json.loads(records[0].read_text())
    assert registered['schemaVersion'] == 3
    assert registered['data']['path'] == str(project)
    assert registered['data']['commands'][0]['configuration']['arguments'] == ['literal ; $HOME %U', '']
    assert registered['data']['commands'][0]['approval']['fingerprint']
    assert registered['data']['commands'][0]['configuration']['runtime']['environmentFiles'] == ['.env']
    assert registered['data']['commands'][0]['configuration']['runtime']['delaySeconds'] == 3
    assert registered['data']['commands'][0]['configuration']['runtime']['stop']['signal'] == 'SIGINT'
    assert registered['data']['preferences']['previewUrl'] == 'http://localhost:8123/docs'
    assert registered['data']['preferences']['editor']['arguments'] == [str(project)]
    assert registered['data']['preferences']['approval']['fingerprint']
    assert 'never-display-this-inspection-secret' not in records[0].read_text()
    original_record = records[0].read_bytes()
    with Native(temporary) as app:
        app.click('nav button[data-page="projects"]')
        app.until("document.querySelector('article[aria-label=\"Registered project Native API\"]')")
        app.click('.project-library article .secondary-button')
        app.until("document.querySelector('#registration-name')")
        assert app.js("return [...document.querySelectorAll('.project-command-editor > fieldset:first-of-type .argument-row input')].map(input => input.value);") == ['literal ; $HOME %U', '']
        assert app.js("return document.querySelector('.project-preferences input[type=url]').value;") == 'http://localhost:8123/docs'
        app.click('.project-runtime-settings summary')
        assert app.js("return document.querySelector('.project-runtime-settings input[name=\"launch-delay\"]').value === '3';")
        assert app.js("return document.querySelector('.project-runtime-settings input[type=url]').value;") == 'http://127.0.0.1:8123/health'
        assert app.js("return document.querySelector('select[name=\"stop-signal\"]').value;") == 'SIGINT'
        app.click('dialog button[type="submit"]')
        app.until("document.querySelector('[aria-label=\"Registration review\"]')")
        project_tool.write_text('#!/bin/sh\n# Changed while reviewing\ntouch ' + shlex.quote(str(project / 'must-not-run')) + '\n')
        app.click('dialog .dialog-actions .primary-button')
        app.until("document.querySelector('dialog [role=alert]')?.textContent.includes('executable changed')")
        assert records[0].read_bytes() == original_record
        app.click('dialog button[type="submit"]')
        app.until("document.querySelector('[aria-label=\"Registration review\"]')")
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog')")
        assert records[0].read_bytes() != original_record
        app.click('.project-library article .secondary-button')
        app.until("document.querySelector('#registration-name')")
        app.click('.project-runtime-settings summary')
        app.narrow_viewport()
        app.assert_layout()
        app.close()
    assert not (project / 'must-not-run').exists()
    assert {path.relative_to(project): path.read_bytes() for path in project.rglob('*') if path.is_file()} == expected_files
    print('PASS: native project inspection, rescan, reviewed registration, literal/empty argv, environment/readiness/delay/preview/editor/stop persistence, reopen and changed-executable rejection; no project execution, secret copy or source write.')
    print("PASS: all persistent test writes stayed in temporary XDG directories; all GUI instances exited cleanly.")

if __name__ == "__main__":
    main()
