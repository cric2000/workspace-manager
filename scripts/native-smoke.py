"""Native Wayland profiles, applications, project inspection and registration checks.

Build with: go build -tags production,mcp -buildvcs=false -o bin/workspace-manager-smoke .
Run from a graphical Wayland session: python3 scripts/native-smoke.py
For project-only iteration: python3 scripts/native-smoke.py --projects-only
For starter creation: python3 scripts/native-smoke.py --creation-only
For Node creation: python3 scripts/native-smoke.py --node-creation-only
For Python creation: python3 scripts/native-smoke.py --python-creation-only
For FastAPI creation: python3 scripts/native-smoke.py --fastapi-creation-only
For restart recovery: python3 scripts/native-smoke.py --recovery-only
For reviewed setup: python3 scripts/native-smoke.py --setup-only (downloads Node dependencies)
For Next.js creation: python3 scripts/native-smoke.py --next-creation-only (downloads the pinned official scaffolder)
All app data and project fixtures are temporary. The normal production binary has
no MCP endpoint. No project commands run here; application tests use owned helpers.
"""

import argparse
import base64
import contextlib
import hashlib
import http.client
import json
import os
from pathlib import Path
import secrets
import shlex
import socket
import signal
import struct
import subprocess
import sys
import tempfile
import time
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
CAPTURE_SCREENSHOTS = False

def controller_rpc(temporary, request):
    directory = temporary / 'state/workspace-manager/runtime/controller'
    descriptor = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
    try:
        with socket.socket(socket.AF_UNIX) as connection:
            connection.settimeout(20)
            connection.connect(f'/proc/self/fd/{descriptor}/control.sock')
            connection.sendall(json.dumps(request).encode() + b'\n')
            connection.shutdown(socket.SHUT_WR)
            with connection.makefile('r') as response:
                return json.load(response)
    finally:
        os.close(descriptor)

@contextlib.contextmanager
def fixture_directory():
    with tempfile.TemporaryDirectory(prefix='workspace-native-check-') as directory:
        try:
            yield directory
        finally:
            try:
                controller_rpc(Path(directory), {'action': 'stop_controller'})
            except (OSError, ValueError):
                pass


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
            self.until("document.querySelector('[data-page=workspaces]')")
            self.click('[data-page=workspaces]')
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

    def tool(self, tool_name, **arguments):
        result = self.rpc("tools/call", dict(name=tool_name, arguments=arguments))
        if result.get("isError"):
            raise RuntimeError(str(result))
        text = result["content"][0]["text"]
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            return text

    def js(self, source):
        return self.tool("js_eval", js=source)

    def until(self, expression, timeout=12):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.js("return Boolean(" + expression + ");"):
                return
            time.sleep(0.1)
        raise AssertionError(expression + "\n" + str(self.js("return {width:innerWidth, height:innerHeight, text:document.body.innerText};")))

    def click(self, selector):
        self.tool("mouse_click", selector=selector)

    def navigate(self, page):
        if page in ['applications', 'diagnostics']:
            self.click('[data-page=settings]')
            self.until("document.querySelector('[data-page=" + page + "]')")
        self.click('[data-page=' + page + ']')

    def more(self, name):
        self.click('summary[aria-label=' + json.dumps('More actions for ' + name) + ']')

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

    def screenshot(self, name):
        if CAPTURE_SCREENSHOTS:
            encoded = self.tool("call_bound_method", name="main.ScreenshotService.Capture", args=[])
            output = ROOT / "docs/screenshots" / (name + ".png")
            output.parent.mkdir(parents=True, exist_ok=True)
            output.write_bytes(base64.b64decode(encoded))

    def close(self):
        try:
            self.tool("window_control", action="close")
        except (http.client.RemoteDisconnected, ConnectionResetError):
            pass  # The final window can close its endpoint before replying.
        assert self.process.wait(timeout=8) == 0


def main():
    global CAPTURE_SCREENSHOTS
    sys.stdout.reconfigure(line_buffering=True)
    parser = argparse.ArgumentParser()
    parser.add_argument("--profiles-only", action="store_true", help="Run workspace and desktop/custom application regression checks only")
    parser.add_argument("--projects-only", action="store_true", help="Run the isolated project inspection/settings workflow")
    parser.add_argument("--creation-only", action="store_true", help="Run all five isolated starter creation workflows (Next.js downloads its pinned scaffolder)")
    parser.add_argument("--node-creation-only", action="store_true", help="Run the isolated Node.js + TypeScript creation workflow")
    parser.add_argument("--python-creation-only", action="store_true", help="Run the isolated Python creation workflow")
    parser.add_argument("--fastapi-creation-only", action="store_true", help="Run the isolated FastAPI creation and local-interpreter approval workflow")
    parser.add_argument("--next-creation-only", action="store_true", help="Run Next.js command approval, official preparation and final publication; downloads create-next-app")
    parser.add_argument("--setup-only", action="store_true", help="Run reviewed Git/npm/venv setup on temporary generated projects; downloads Node dependencies")
    parser.add_argument("--recovery-only", action="store_true", help="Run isolated interrupted publication recovery and guarded registration restoration")
    parser.add_argument("--lifecycle-only", action="store_true", help="Run an owned managed server, controller crash/reconnect, snapshot branching and shutdown cancellation; never power off")
    parser.add_argument("--login-only", action="store_true", help="Verify cold-controller login and session restore for all startup modes using only an owned non-D-Bus desktop helper")
    parser.add_argument("--ui-only", action="store_true", help="Check global creation, quick actions, suggested session names, themes and responsive navigation")
    parser.add_argument("--screenshots", action="store_true", help="Capture actual native lifecycle UI into docs/screenshots")
    arguments = parser.parse_args()
    CAPTURE_SCREENSHOTS = arguments.screenshots
    with fixture_directory() as directory:
        temporary = Path(directory)
        if arguments.login_only:
            check_login(temporary)
            return
        if arguments.ui_only:
            check_ui(temporary)
            return
        if arguments.lifecycle_only:
            check_lifecycle(temporary)
            return
        if arguments.recovery_only:
            check_recovery(temporary)
            return
        if arguments.setup_only:
            check_setup(temporary)
            return
        if arguments.next_creation_only:
            check_next_creation(temporary)
            return
        if arguments.fastapi_creation_only:
            check_fastapi_creation(temporary)
            return
        if arguments.python_creation_only:
            check_python_creation(temporary)
            return
        if arguments.node_creation_only:
            check_node_creation(temporary)
            return
        if arguments.creation_only:
            check_creation(temporary)
            check_node_creation(temporary)
            check_python_creation(temporary)
            check_fastapi_creation(temporary)
            check_next_creation(temporary)
            return
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
            app.more('Backend')
            app.click('button[aria-label="Duplicate Backend"]')
            app.until("document.querySelector('#workspace-name')")
            app.fill("#workspace-name", "Learning")
            app.submit()
            app.until("document.querySelectorAll('article').length === 2")
            app.more('Backend')
            app.click('button[aria-label="Set Backend as default"]')
            app.until("document.querySelector('article[aria-label=\"Backend\"] .default-badge')")
            app.more('Backend')
            app.click('button[aria-label="Delete Backend"]')
            app.until("document.querySelector('dialog')")
            # A synthetic cancel event verifies our handler; OS Escape remains manual.
            app.js("document.querySelector('dialog').dispatchEvent(new Event('cancel', {cancelable:true})); return true;")
            app.until("!document.querySelector('dialog')")
            assert app.js("return document.activeElement.getAttribute('aria-label');") == "More actions for Backend"
            app.more('Backend')
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
            app.navigate('applications')
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

            app.navigate('diagnostics')
            app.until("document.body.innerText.includes('Desktop bridge connected')")
            assert "wayland" in app.js("return document.body.innerText;")
            app.click(".environment-panel .secondary-button")
            app.until("document.body.innerText.includes('Desktop bridge connected')")
            app.navigate('workspaces')
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
        assert all(json.loads(path.read_text())["schemaVersion"] == 4 for path in profiles.glob("*.json"))
        settings = json.loads((temporary / "config/workspace-manager/settings.json").read_text())["data"]
        assert settings["defaultWorkspaceId"] == records["Backend"]["id"]
        assert not settings["autostart"]

        with Native(temporary) as app:
            app.until("document.querySelectorAll('article').length === 2")
            assert app.js("return !!document.querySelector('article[aria-label=\"Backend\"] .default-badge');")
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
            app.more('Learning')
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
            app.navigate('applications')
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
        if arguments.profiles_only:
            return
        check_projects(temporary)
        check_creation(temporary)
        check_node_creation(temporary)
        check_python_creation(temporary)
        check_fastapi_creation(temporary)
        check_next_creation(temporary)
        check_setup(temporary)


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
        app.navigate('projects')
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
        app.navigate('projects')
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
    # Change a detected entry point after configuration was approved. Rescan and
    # draft selection must leave exact saved bytes intact until explicit approval.
    (project / 'cmd/api').rename(project / 'cmd/updated')
    expected_files[Path('cmd/updated/main.go')] = expected_files.pop(Path('cmd/api/main.go'))
    before_rescan = records[0].read_bytes()
    previous = json.loads(before_rescan)['data']
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('.project-rescan-button')")
        app.click('.project-rescan-button')
        app.until("document.querySelector('select[name=\"rescan-candidate\"]')")
        assert records[0].read_bytes() == before_rescan
        assert app.js("return document.querySelector('dialog .dialog-actions .primary-button').disabled;")
        app.js("const field = document.querySelector('select[name=\"rescan-candidate\"]'); field.value = [...field.options].find(option => option.textContent === 'Go: ./cmd/updated').value; field.dispatchEvent(new Event('change', {bubbles:true})); return true;")
        app.until("document.querySelector('select[name=\"rescan-target\"]')")
        app.js("const field = document.querySelector('select[name=\"rescan-target\"]'); field.value = " + json.dumps(previous['commands'][0]['id']) + "; field.dispatchEvent(new Event('change', {bubbles:true})); return true;")
        app.until("document.querySelector('section[aria-label=\"Candidate change comparison\"]')")
        assert app.js("return document.querySelector('dialog table').textContent.includes('literal ; $HOME %U') && document.querySelector('dialog table').textContent.includes('./cmd/updated');")
        assert not app.js("return document.querySelector('dialog').textContent.includes('never-display-this-inspection-secret');")
        app.click('dialog .dialog-actions .primary-button')
        app.until("document.querySelector('#registration-name')")
        assert records[0].read_bytes() == before_rescan
        assert app.js("return [...document.querySelectorAll('.project-command-editor > fieldset:first-of-type .argument-row input')].map(input => input.value);") == ['run', './cmd/updated']
        assert app.js("return document.querySelector('.project-preferences input[type=url]').value;") == previous['preferences']['previewUrl']
        app.click('dialog button[type="submit"]')
        app.until("document.querySelector('[aria-label=\"Registration review\"]')")
        assert records[0].read_bytes() == before_rescan
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog')")
        updated = json.loads(records[0].read_text())['data']
        assert updated['id'] == previous['id']
        assert updated['commands'][0]['id'] == previous['commands'][0]['id']
        assert updated['commands'][0]['configuration']['name'] == previous['commands'][0]['configuration']['name']
        assert updated['commands'][0]['configuration']['arguments'] == ['run', './cmd/updated']
        assert updated['commands'][0]['configuration']['runtime'] == previous['commands'][0]['configuration']['runtime']
        assert updated['preferences']['previewUrl'] == previous['preferences']['previewUrl']
        assert updated['preferences']['editor'] == previous['preferences']['editor']
        assert updated['commands'][0]['approval']['fingerprint'] != previous['commands'][0]['approval']['fingerprint']
        after_save = records[0].read_bytes()
        app.click('.project-rescan-button')
        app.until("document.querySelector('select[name=\"rescan-candidate\"]')")
        assert app.js("return document.querySelector('ul[aria-label=\"Saved command matches\"]').textContent.includes('Equivalent invocation detected');")
        app.js("const field = document.querySelector('select[name=\"rescan-candidate\"]'); field.value = [...field.options].find(option => option.textContent === 'Go: ./cmd/updated').value; field.dispatchEvent(new Event('change', {bubbles:true})); return true;")
        app.until("document.querySelector('select[name=\"rescan-target\"]')")
        app.js("const field = document.querySelector('select[name=\"rescan-target\"]'); field.value = " + json.dumps(updated['commands'][0]['id']) + "; field.dispatchEvent(new Event('change', {bubbles:true})); return true;")
        app.until("document.querySelector('section[aria-label=\"Candidate change comparison\"]')")
        app.narrow_viewport()
        app.assert_layout()
        app.close()
        assert records[0].read_bytes() == after_save
    # Associate, duplicate and reopen shared references before guarded removal.
    with Native(temporary) as app:
        app.click('[data-new-workspace]')
        app.until("document.querySelector('#workspace-name')")
        app.fill('#workspace-name', 'Native Project Workspace')
        app.submit()
        app.click('button[aria-label="Configure projects for Native Project Workspace"]')
        app.until("document.querySelector('button[aria-label=\"Add Native API to workspace\"]')")
        app.click('button[aria-label="Add Native API to workspace"]')
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog')")
        app.more('Native Project Workspace')
        app.click('button[aria-label="Duplicate Native Project Workspace"]')
        app.until("document.querySelector('#workspace-name')")
        app.fill('#workspace-name', 'Native Project Copy')
        app.submit()
        app.close()
    profiles = temporary / 'config/workspace-manager/profiles'
    linked = [json.loads(path.read_text())['data'] for path in profiles.glob('*.json')
              if json.loads(path.read_text())['data']['name'].startswith('Native Project ')]
    assert len(linked) == 2 and all(item['projectIds'] == [updated['id']] for item in linked)
    assert records[0].read_bytes() == after_save
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('article[aria-label=\"Registered project Native API\"]')")
        app.click('.project-library article .delete-button')
        app.until("document.querySelector('dialog .danger-button')")
        app.click('dialog .danger-button')
        app.until("document.querySelector('dialog [role=alert]')?.textContent.includes('Native Project Workspace')")
        assert app.js("return document.querySelector('dialog [role=alert]').textContent.includes('Native Project Copy') && document.querySelector('dialog .danger-button').disabled;")
        assert records[0].read_bytes() == after_save
        app.click('dialog .dialog-actions .secondary-button')
        app.navigate('workspaces')
        for name in ['Native Project Workspace', 'Native Project Copy']:
            selector = 'button[aria-label="Configure projects for ' + name + '"]'
            app.until('document.querySelector(' + json.dumps(selector) + ')')
            app.click(selector)
            app.until("document.querySelector('button[aria-label=\"Remove Native API from workspace\"]') && !document.querySelector('dialog .dialog-actions .primary-button').disabled")
            app.click('button[aria-label="Remove Native API from workspace"]')
            app.click('dialog .dialog-actions .primary-button')
            app.until("!document.querySelector('dialog')")
        app.navigate('projects')
        app.until("document.querySelector('article[aria-label=\"Registered project Native API\"]')")
        app.click('.project-library article .delete-button')
        app.until("document.querySelector('dialog .danger-button')")
        app.click('dialog .danger-button')
        app.until("!document.querySelector('dialog') && !document.querySelector('article[aria-label=\"Registered project Native API\"]')")
        assert not records[0].exists()
        app.navigate('workspaces')
        app.until("document.querySelector('button[aria-label=\"Configure projects for Native Project Workspace\"]')")
        app.click('button[aria-label="Configure projects for Native Project Workspace"]')
        app.until("document.querySelector('dialog .picker-empty')")
        app.narrow_viewport()
        app.assert_layout()
        app.close()
    assert not list(project_records.glob('*.json'))
    assert after_save in [path.read_bytes() for path in (project_records / 'history' / updated['id']).glob('*.json')]
    assert not (project / 'must-not-run').exists()
    assert {path.relative_to(project): path.read_bytes() for path in project.rglob('*') if path.is_file()} == expected_files
    print('PASS: native project import/review/rescan, workspace association/duplication/reopen, guarded metadata removal and narrow picker layout; source and approval history preserved, no project execution or secret copy.')
    print("PASS: all persistent test writes stayed in temporary XDG directories; all GUI instances exited cleanly.")

def check_creation(temporary):
    parent = temporary / 'new-projects'
    parent.mkdir()
    target = parent / 'hello-go'
    existing = parent / 'existing'
    existing.mkdir()
    (existing / 'keep.txt').write_text('user files stay exactly here')
    records = temporary / 'config/workspace-manager/projects'
    before_records = {path.name: path.read_bytes() for path in records.glob('*.json')}
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('h1#projects-title')")
        app.js("[...document.querySelectorAll('button')].find(button => button.textContent === 'New project').click(); return true;")
        app.until("document.querySelector('#creation-name')?.matches(':enabled') && document.querySelector('#creation-template option')")
        assert app.js("return document.activeElement.id === 'creation-name';")
        app.fill('#creation-name', 'existing')
        app.fill('#creation-parent', str(parent))
        app.fill('#creation-module', 'example.com/native/hello-go')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('dialog [role=alert]')?.textContent.includes('destination already exists')")
        assert (existing / 'keep.txt').read_text() == 'user files stay exactly here'
        app.fill('#creation-name', 'hello-go')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        assert not target.exists()
        assert app.js("return [...document.querySelectorAll('.project-creation-files summary')].map(item => item.textContent);") == ['go.mod', 'main.go', '.gitignore', 'README.md']
        assert app.js("return document.querySelector('section[aria-label=\"Suggested startup command\"]').textContent.includes('review required');")
        app.click('dialog .dialog-actions .secondary-button')
        app.until("document.querySelector('#creation-module')")
        app.fill('#creation-module', 'example.com/native/reviewed-go')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        assert not target.exists()
        app.click('dialog .dialog-actions .primary-button')
        app.until("document.querySelector('ol[aria-label=\"Creation activity\"]')")
        assert app.js("return document.querySelector('dialog').textContent.includes('Created and registered') && document.querySelector('dialog').textContent.includes('No project code was executed');")
        assert sorted(path.name for path in target.iterdir()) == ['.gitignore', 'README.md', 'go.mod', 'main.go']
        assert 'module example.com/native/reviewed-go' in (target / 'go.mod').read_text()
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog') && document.querySelector('article[aria-label=\"Registered project hello-go\"]')")
        assert app.js("return document.querySelector('article[aria-label=\"Registered project hello-go\"]').textContent.includes('Review required');")
        app.close()
    created_records = [path for path in records.glob('*.json') if path.name not in before_records]
    assert len(created_records) == 1
    saved = json.loads(created_records[0].read_text())['data']
    assert saved['source'] == 'bootstrapped' and saved['path'] == str(target)
    assert saved['commands'][0]['configuration']['arguments'] == ['run', '.']
    assert not saved['commands'][0].get('approval')
    source = {path.name: path.read_bytes() for path in target.iterdir()}
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('article[aria-label=\"Registered project hello-go\"]')")
        app.js("[...document.querySelectorAll('button')].find(button => button.textContent === 'New project').click(); return true;")
        app.until("document.querySelector('#creation-name')?.matches(':enabled') && document.querySelector('#creation-template option')")
        app.fill('#creation-name', 'preview-only')
        app.fill('#creation-parent', str(parent))
        app.fill('#creation-module', 'example.com/native/preview-only')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        app.js("document.querySelectorAll('.project-creation-files details').forEach(element => element.open = true); return true;")
        app.narrow_viewport()
        app.assert_layout()
        app.close()
    assert not (parent / 'preview-only').exists()
    assert {path.name: path.read_bytes() for path in target.iterdir()} == source
    assert (existing / 'keep.txt').read_text() == 'user files stay exactly here'
    assert not list(parent.glob('.workspace-manager-*'))
    assert all((records / name).read_bytes() == data for name, data in before_records.items())
    print('PASS: native Go creation preview/edit/confirm, existing destination protection, bootstrapped registration/reopen, unapproved startup command and narrow file preview; no project execution or overwrite.')


def check_node_creation(temporary):
    parent = temporary / 'node-projects'
    parent.mkdir()
    target = parent / 'hello-node'
    records = temporary / 'config/workspace-manager/projects'
    before_records = {path.name: path.read_bytes() for path in records.glob('*.json')}

    def options(app, name):
        app.js("[...document.querySelectorAll('button')].find(button => button.textContent === 'New project').click(); return true;")
        app.until("document.querySelector('#creation-name')?.matches(':enabled') && document.querySelector('#creation-template option[value=\"node-typescript\"]')")
        app.js("const select = document.querySelector('#creation-template'); select.value = 'node-typescript'; select.dispatchEvent(new Event('change', {bubbles:true})); return true;")
        app.until("!document.querySelector('#creation-module')")
        app.fill('#creation-name', name)
        app.fill('#creation-parent', str(parent))

    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('h1#projects-title')")
        options(app, 'Uppercase_Node')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('dialog [role=alert]')?.textContent.includes('lowercase')")
        assert not list(parent.iterdir())
        app.fill('#creation-name', 'hello-node')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        assert not target.exists()
        assert app.js("return [...document.querySelectorAll('.project-creation-files summary')].map(item => item.textContent);") == ['package.json', 'tsconfig.json', 'src/index.ts', '.gitignore', 'README.md']
        assert app.js("return document.querySelector('section[aria-label=\"Separate dependency setup\"]').textContent.includes('No packages will be installed') && document.querySelector('section[aria-label=\"Separate dependency setup\"]').textContent.includes('--ignore-scripts');")
        app.click('dialog button[aria-label="Close dialog"]')
        app.until("!document.querySelector('dialog')")
        assert not target.exists()
        options(app, 'hello-node')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        app.click('dialog .dialog-actions .primary-button')
        app.until("document.querySelector('ol[aria-label=\"Creation activity\"]')")
        assert app.js("return document.querySelector('dialog').textContent.includes('Generated 5 reviewed files') && document.querySelector('dialog').textContent.includes('Created and registered');")
        assert not (target / 'node_modules').exists() and not (target / 'package-lock.json').exists()
        # Closing the result must refresh registration state just like Done.
        app.click('dialog button[aria-label="Close dialog"]')
        app.until("!document.querySelector('dialog') && document.querySelector('article[aria-label=\"Registered project hello-node\"]')")
        app.close()
    created = [path for path in records.glob('*.json') if path.name not in before_records]
    assert len(created) == 1
    saved = json.loads(created[0].read_text())['data']
    assert saved['source'] == 'bootstrapped' and saved['path'] == str(target)
    assert saved['commands'][0]['configuration']['arguments'] == ['--watch', 'src/index.ts']
    assert not saved['commands'][0].get('approval')
    source = {str(path.relative_to(target)): path.read_bytes() for path in target.rglob('*') if path.is_file()}
    assert sorted(source) == ['.gitignore', 'README.md', 'package.json', 'src/index.ts', 'tsconfig.json']
    package = json.loads(source['package.json'])
    assert package['private'] and package['scripts']['dev'] == 'node --watch src/index.ts'
    assert package['devDependencies']['typescript'] == '7.0.2'
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('article[aria-label=\"Registered project hello-node\"]')")
        app.click('article[aria-label="Registered project hello-node"] .secondary-button')
        app.until("document.querySelector('#registration-name')")
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('[aria-label=\"Registration review\"]')")
        assert app.js("return [...document.querySelectorAll('dialog .project-command-editor > .launch-review li')].map(item => item.textContent);") == ['"--watch"', '"src/index.ts"']
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog')")
        approved = json.loads(created[0].read_text())['data']
        assert approved['source'] == 'bootstrapped' and approved['commands'][0]['approval']['fingerprint']
        options(app, 'preview-only-node')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Separate dependency setup\"]')")
        app.js("document.querySelectorAll('.project-creation-files details').forEach(element => element.open = true); return true;")
        app.narrow_viewport()
        app.assert_layout()
        app.close()
    assert not (parent / 'preview-only-node').exists()
    assert {str(path.relative_to(target)): path.read_bytes() for path in target.rglob('*') if path.is_file()} == source
    assert not list(parent.glob('.workspace-manager-*'))
    assert all((records / name).read_bytes() == data for name, data in before_records.items())
    print('PASS: native Node/TypeScript selection/naming, exact five-file preview, separate npm setup, nested creation/reopen and later command approval; preview/cancel preserve source and no dependencies or project commands run.')


def check_python_creation(temporary):
    parent = temporary / 'python-projects'
    parent.mkdir()
    target = parent / 'hello-python'
    records = temporary / 'config/workspace-manager/projects'
    before_records = {path.name: path.read_bytes() for path in records.glob('*.json')}

    def options(app, name):
        app.js("[...document.querySelectorAll('button')].find(button => button.textContent === 'New project').click(); return true;")
        app.until("document.querySelector('#creation-name')?.matches(':enabled') && document.querySelector('#creation-template option[value=\"python\"]')")
        app.js("const select = document.querySelector('#creation-template'); select.value = 'python'; select.dispatchEvent(new Event('change', {bubbles:true})); return true;")
        app.until("!document.querySelector('#creation-module')")
        app.fill('#creation-name', name)
        app.fill('#creation-parent', str(parent))

    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('h1#projects-title')")
        options(app, 'trailing-')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('dialog [role=alert]')?.textContent.includes('ending with a letter or digit')")
        assert not list(parent.iterdir())
        app.fill('#creation-name', 'hello-python')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        assert not target.exists()
        assert app.js("return [...document.querySelectorAll('.project-creation-files summary')].map(item => item.textContent);") == ['pyproject.toml', 'main.py', 'tests/test_main.py', '.gitignore', 'README.md']
        assert app.js("return document.querySelector('section[aria-label=\"Separate optional virtual environment\"]').textContent.includes('not run') && document.querySelector('section[aria-label=\"Separate optional virtual environment\"]').textContent.includes('.venv');")
        app.click('dialog button[aria-label="Close dialog"]')
        app.until("!document.querySelector('dialog')")
        assert not target.exists()
        options(app, 'hello-python')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        app.click('dialog .dialog-actions .primary-button')
        app.until("document.querySelector('ol[aria-label=\"Creation activity\"]')")
        assert app.js("return document.querySelector('dialog').textContent.includes('Generated 5 reviewed files') && document.querySelector('dialog').textContent.includes('Created and registered');")
        for name in ['.venv', '__pycache__', 'tests/__pycache__', 'build', 'dist']:
            assert not (target / name).exists(), name
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog') && document.querySelector('article[aria-label=\"Registered project hello-python\"]')")
        app.close()
    created = [path for path in records.glob('*.json') if path.name not in before_records]
    assert len(created) == 1
    saved = json.loads(created[0].read_text())['data']
    assert saved['source'] == 'bootstrapped' and saved['path'] == str(target)
    assert saved['commands'][0]['configuration']['arguments'] == ['-B', 'main.py']
    assert not saved['commands'][0].get('approval')
    source = {str(path.relative_to(target)): path.read_bytes() for path in target.rglob('*') if path.is_file()}
    assert sorted(source) == ['.gitignore', 'README.md', 'main.py', 'pyproject.toml', 'tests/test_main.py']
    assert b'requires-python = ">=3.11"' in source['pyproject.toml']
    assert b'python3 -m venv .venv' in source['README.md']
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('article[aria-label=\"Registered project hello-python\"]')")
        app.click('article[aria-label="Registered project hello-python"] .secondary-button')
        app.until("document.querySelector('#registration-name')")
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('[aria-label=\"Registration review\"]')")
        assert app.js("return [...document.querySelectorAll('dialog .project-command-editor > .launch-review li')].map(item => item.textContent);") == ['"-B"', '"main.py"']
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog')")
        approved = json.loads(created[0].read_text())['data']
        assert approved['source'] == 'bootstrapped' and approved['commands'][0]['approval']['fingerprint']
        options(app, 'preview-only-python')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Separate optional virtual environment\"]')")
        app.js("document.querySelectorAll('.project-creation-files details').forEach(element => element.open = true); return true;")
        app.narrow_viewport()
        app.assert_layout()
        app.close()
    assert not (parent / 'preview-only-python').exists()
    assert {str(path.relative_to(target)): path.read_bytes() for path in target.rglob('*') if path.is_file()} == source
    assert not list(parent.glob('.workspace-manager-*'))
    assert all((records / name).read_bytes() == data for name, data in before_records.items())
    print('PASS: native Python selection/naming, exact five-file preview, separate optional venv step, creation/reopen and later command approval; cancellation preserves source and no Python or environment setup runs during creation.')



def check_fastapi_creation(temporary):
    parent = temporary / 'fastapi-projects'
    parent.mkdir()
    target = parent / 'hello-api'
    records = temporary / 'config/workspace-manager/projects'
    before_records = {path.name: path.read_bytes() for path in records.glob('*.json')}

    def options(app, name):
        app.js("[...document.querySelectorAll('button')].find(button => button.textContent === 'New project').click(); return true;")
        app.until("document.querySelector('#creation-name')?.matches(':enabled') && document.querySelector('#creation-template option[value=\"fastapi\"]')")
        app.js("const select = document.querySelector('#creation-template'); select.value = 'fastapi'; select.dispatchEvent(new Event('change', {bubbles:true})); return true;")
        app.until("!document.querySelector('#creation-module')")
        app.fill('#creation-name', name)
        app.fill('#creation-parent', str(parent))

    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('h1#projects-title')")
        options(app, 'hello-api')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        assert not target.exists()
        assert app.js("return [...document.querySelectorAll('.project-creation-files summary')].map(item => item.textContent);") == ['pyproject.toml', 'app/__init__.py', 'app/main.py', 'tests/test_api.py', '.gitignore', 'README.md']
        assert app.js("return document.querySelector('section[aria-label=\"Separate install fastapi dependencies\"]').textContent.includes('.[test]') && document.querySelector('section[aria-label=\"Suggested startup command\"]').textContent.includes('HTTP 200: http://127.0.0.1:8000/health');")
        app.click('dialog button[aria-label="Close dialog"]')
        app.until("!document.querySelector('dialog')")
        assert not target.exists()
        options(app, 'hello-api')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        app.click('dialog .dialog-actions .primary-button')
        app.until("document.querySelector('ol[aria-label=\"Creation activity\"]')")
        assert app.js("return document.querySelector('dialog').textContent.includes('Generated 6 reviewed files') && document.querySelector('dialog').textContent.includes('Created and registered');")
        assert not (target / '.venv').exists()
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog') && document.querySelector('article[aria-label=\"Registered project hello-api\"]')")
        app.click('article[aria-label="Registered project hello-api"] .secondary-button')
        app.until("document.querySelector('#registration-name')")
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('dialog [role=alert]')?.textContent.includes('unavailable')")
        app.click('dialog button[aria-label="Close dialog"]')
        app.until("!document.querySelector('dialog')")
        app.close()
    created = [path for path in records.glob('*.json') if path.name not in before_records]
    assert len(created) == 1
    saved = json.loads(created[0].read_text())['data']
    config = saved['commands'][0]['configuration']
    interpreter = target / '.venv/bin/python'
    assert saved['source'] == 'bootstrapped' and config['executable'] == str(interpreter)
    assert not saved['commands'][0].get('approval')
    assert config['runtime']['readiness']['url'] == 'http://127.0.0.1:8000/health'
    assert saved['preferences']['previewUrl'] == 'http://127.0.0.1:8000/docs'
    source = {str(path.relative_to(target)): path.read_bytes() for path in target.rglob('*') if path.is_file()}
    # Test-owned manual environment setup only, without pip or dependencies. This
    # makes the interpreter reviewable; no server or generated source is run.
    subprocess.run([sys.executable, '-m', 'venv', '--without-pip', str(target / '.venv')], check=True, timeout=20)
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('article[aria-label=\"Registered project hello-api\"]')")
        app.click('article[aria-label="Registered project hello-api"] .secondary-button')
        app.until("document.querySelector('#registration-name')")
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('[aria-label=\"Registration review\"]')")
        assert app.js("return document.querySelector('[aria-label=\"Registration review\"]').textContent.includes('.venv/bin/python');")
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog')")
        approved = json.loads(created[0].read_text())['data']
        assert approved['source'] == 'bootstrapped' and approved['commands'][0]['approval']['fingerprint']
        assert approved['commands'][0]['configuration']['executable'] == str(interpreter)
        options(app, 'preview-only-api')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Separate install fastapi dependencies\"]')")
        app.js("document.querySelectorAll('.project-creation-files details').forEach(element => element.open = true); return true;")
        app.narrow_viewport()
        app.assert_layout()
        app.close()
    assert not (parent / 'preview-only-api').exists()
    assert all((target / name).read_bytes() == data for name, data in source.items())
    assert not list(target.rglob('__pycache__'))
    assert not list(parent.glob('.workspace-manager-*'))
    assert all((records / name).read_bytes() == data for name, data in before_records.items())
    print('PASS: native FastAPI six-file preview/cancellation, two manual setup steps, health/docs settings, creation/reopen, missing-venv rejection, later venv interpreter approval and narrow layout; no dependencies or project source executed.')



def check_next_creation(temporary):
    parent = temporary / 'next-projects'
    parent.mkdir()
    target = parent / 'hello-next'
    records = temporary / 'config/workspace-manager/projects'
    before_records = {path.name: path.read_bytes() for path in records.glob('*.json')}
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('h1#projects-title')")
        app.js("[...document.querySelectorAll('button')].find(button => button.textContent === 'New project').click(); return true;")
        app.until("document.querySelector('#creation-name')?.matches(':enabled') && document.querySelector('#creation-template option[value=\"nextjs\"]')")
        app.js("const select=document.querySelector('#creation-template'); select.value='nextjs'; select.dispatchEvent(new Event('change',{bubbles:true})); return true;")
        app.until("document.querySelector('fieldset legend')?.textContent === 'Next.js options'")
        app.fill('#creation-name', 'hello-next')
        app.fill('#creation-parent', str(parent))
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Official scaffolder review\"]')")
        assert not target.exists()
        assert not app.js("return Boolean(document.querySelector('section[aria-label=\"Generated files\"]'));")
        assert app.js("return document.querySelector('section[aria-label=\"Official scaffolder review\"]').textContent.includes('create-next-app@16.4.3') && document.querySelector('section[aria-label=\"Official scaffolder review\"]').textContent.includes('--skip-install');")
        staging = app.js("return [...document.querySelectorAll('section[aria-label=\"Official scaffolder review\"] .launch-review dd')].at(-1).textContent;")
        assert not Path(staging).exists()
        app.click('dialog .dialog-actions .primary-button')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]') || document.querySelector('dialog [role=alert]')", timeout=90)
        assert app.js("return Boolean(document.querySelector('section[aria-label=\"Generated files\"]'));"), app.js("return document.querySelector('dialog').textContent;")
        assert not target.exists() and not Path(staging).exists()
        names = app.js("return [...document.querySelectorAll('.project-creation-files summary')].map(item=>item.textContent);")
        assert 'src/app/page.tsx' in names and 'package.json' in names and 'tsconfig.json' in names
        assert 'AGENTS.md' not in names
        app.click('dialog .dialog-actions .primary-button')
        app.until("document.querySelector('ol[aria-label=\"Creation activity\"]')")
        assert not (target / 'node_modules').exists() and not (target / '.git').exists()
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog') && document.querySelector('article[aria-label=\"Registered project hello-next\"]')")
        app.close()
    created = [path for path in records.glob('*.json') if path.name not in before_records]
    assert len(created) == 1
    saved = json.loads(created[0].read_text())['data']
    assert saved['source'] == 'bootstrapped' and not saved['commands'][0].get('approval')
    assert saved['preferences']['previewUrl'] == 'http://127.0.0.1:3000'
    assert saved['commands'][0]['configuration']['arguments'] == ['run','dev','--','--hostname','127.0.0.1','--port','3000']
    history_dir = temporary / 'state/workspace-manager/creation-history'
    history = [json.loads(path.read_text())['data'] for path in history_dir.glob('*.json')]
    assert len(history) == 2 and {log['kind'] for log in history} == {'preparation','publication'}
    assert all(log['result']['succeeded'] for log in history)
    preparation = next(log for log in history if log['kind'] == 'preparation')
    assert preparation['result']['processStarted'] and preparation['result']['exitCode'] == 0
    assert preparation['result']['output']
    review_token = Path(staging).name.removeprefix('workspace-manager-next-')
    assert all(review_token not in path.read_text() for path in history_dir.rglob('*.json'))
    # A failed attempt without a registration must also be visible after restart.
    failed = dict(id=secrets.token_hex(16),projectId=secrets.token_hex(16),name='unregistered-fixture',path=str(parent/'unregistered-fixture'),templateId='go',kind='publication',recordedAt=preparation['recordedAt'],finishedAt=preparation['recordedAt'],result=dict(succeeded=False,created=False,registered=False,processStarted=False,exitCode=-1,fileCount=4,activity=[],output='',truncated=False,code='destination_exists',message='Existing destination was kept.'))
    failed_path = history_dir / (failed['id'] + '.json')
    failed_path.write_text(json.dumps(dict(schemaVersion=1,kind='creation-log',data=failed)))
    failed_path.chmod(0o600)
    history_before = {path:path.read_bytes() for path in history_dir.glob('*.json')}
    source = {str(path.relative_to(target)):path.read_bytes() for path in target.rglob('*') if path.is_file()}
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('article[aria-label=\"Registered project hello-next\"]')")
        app.js("[...document.querySelectorAll('button')].find(button=>button.textContent === 'Creation history').click(); return true;")
        app.until("document.querySelector('[aria-label=\"hello-next preparation history\"]')")
        assert app.js("return document.querySelector('[aria-label=\"hello-next preparation history\"]').textContent.includes('Tool process started: yes. Exit code: 0');")
        assert app.js("return document.querySelector('[aria-label=\"hello-next publication history\"]').textContent.includes('Files published: yes. Registration saved: yes');")
        assert app.js("return document.querySelector('[aria-label=\"unregistered-fixture publication history\"]').textContent.includes('Existing destination was kept.');")
        assert all(path.read_bytes() == data for path,data in history_before.items())
        assert len(list(history_dir.glob('*.json'))) == 3
        app.narrow_viewport()
        app.assert_layout()
        app.js("[...document.querySelectorAll('dialog button')].find(button=>button.textContent === 'Done').click(); return true;")
        app.until("!document.querySelector('dialog')")
        app.click('article[aria-label="Registered project hello-next"] .secondary-button')
        app.until("document.querySelector('#registration-name')")
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('[aria-label=\"Registration review\"]')")
        app.click('dialog .dialog-actions .primary-button')
        app.until("!document.querySelector('dialog')")
        approved = json.loads(created[0].read_text())['data']
        assert approved['commands'][0]['approval']['fingerprint']
        app.js("[...document.querySelectorAll('button')].find(button=>button.textContent === 'New project').click(); return true;")
        app.until("document.querySelector('#creation-name')?.matches(':enabled')")
        app.js("const select=document.querySelector('#creation-template'); select.value='nextjs'; select.dispatchEvent(new Event('change',{bubbles:true})); return true;")
        app.fill('#creation-name','preview-only-next')
        app.fill('#creation-parent',str(parent))
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Official scaffolder review\"]')")
        app.narrow_viewport()
        app.assert_layout()
        app.close()
    assert not (parent / 'preview-only-next').exists()
    assert {str(path.relative_to(target)):path.read_bytes() for path in target.rglob('*') if path.is_file()} == source
    assert all((records / name).read_bytes() == data for name,data in before_records.items())
    print('PASS: native creation/scaffolding history after restart, saved real tool output and unregistered failure, token redaction/read-only history, Next.js consent and preparation, separate exact-file publication, no dependency/Git setup, unapproved startup/reopen/later approval and narrow command review.')



def check_setup(temporary):
    parent = temporary / 'setup-projects'
    parent.mkdir()
    records = temporary / 'config/workspace-manager/projects'

    def button(app, text):
        app.js("[...document.querySelectorAll('dialog button')].find(button=>button.textContent === " + json.dumps(text) + ").click(); return true;")

    def create(app, template, name):
        app.navigate('projects')
        app.until("document.querySelector('h1#projects-title')")
        app.js("[...document.querySelectorAll('button')].find(button=>button.textContent === 'New project').click(); return true;")
        app.until("document.querySelector('#creation-name')?.matches(':enabled')")
        app.js("const select=document.querySelector('#creation-template');select.value=" + json.dumps(template) + ";select.dispatchEvent(new Event('change',{bubbles:true}));return true;")
        app.fill('#creation-name',name)
        app.fill('#creation-parent',str(parent))
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        button(app,'Check tool versions')
        app.until("document.querySelector('[aria-label=\"Tool version checks\"] [role=status]')")
        text = app.js("return document.querySelector('[aria-label=\"Tool version checks\"] [role=status]').textContent;")
        assert 'supported' in text and 'unconfirmed' not in text and 'unsupported' not in text, text
        assert not (parent / name).exists()
        button(app, 'Confirm and create project')
        app.until("document.querySelector('section[aria-label=\"Optional project setup\"]')")

    def versions(app):
        region = '[aria-label="Tool version checks"]'
        assert not app.js("return !!document.querySelector('" + region + " [role=status]');")
        button(app,'Check tool versions')
        app.until("document.querySelector('" + region + " [role=status]')")
        text = app.js("return document.querySelector('" + region + " [role=status]').textContent;")
        assert 'supported' in text and 'unsupported' not in text and 'unconfirmed' not in text, text
        assert app.js("return ![...document.querySelectorAll('dialog button')].find(button=>button.textContent === 'Run reviewed setup').disabled;")

    target = parent / 'setup-node'
    with Native(temporary) as app:
        create(app,'node-typescript','setup-node')
        source = (target / 'src/index.ts').read_bytes()
        button(app,'Review Git initialization')
        app.until("document.querySelector('[aria-label=\"Setup command review\"]')")
        assert not (target / '.git').exists()
        assert app.js("return document.querySelector('[aria-label=\"Setup command review\"]').textContent.includes('--initial-branch=main');")
        versions(app)
        button(app,'Run reviewed setup')
        app.until("document.querySelector('[aria-label=\"git setup result\"]')?.textContent.includes('completed')")
        assert (target / '.git/HEAD').read_text() == 'ref: refs/heads/main\n'
        assert not (target / '.git/index').exists()
        button(app,'Review dependency installation')
        app.until("document.querySelector('[aria-label=\"Setup command review\"]')")
        assert not (target / 'node_modules').exists()
        assert app.js("return document.querySelector('[aria-label=\"Setup command review\"]').textContent.includes('--ignore-scripts') && document.querySelector('[aria-label=\"Setup command review\"]').textContent.includes('--workspaces=false');")
        versions(app)
        button(app,'Run reviewed setup')
        app.until("document.querySelector('[aria-label=\"dependencies setup result\"]')",timeout=120)
        assert app.js("return document.querySelector('[aria-label=\"dependencies setup result\"]').textContent.includes('completed');"), app.js("return document.querySelector('dialog').textContent;")
        assert (target / 'node_modules/typescript').is_dir() and (target / 'package-lock.json').is_file()
        assert (target / 'src/index.ts').read_bytes() == source
        app.narrow_viewport()
        app.assert_layout()
        app.close()
    node_records = [json.loads(path.read_text())['data'] for path in records.glob('*.json')]
    saved = next(project for project in node_records if project['path'] == str(target))
    assert saved['source'] == 'bootstrapped' and not saved['commands'][0].get('approval')
    # These commands validate only this test-owned generated fixture. The app
    # itself has not approved or run the startup command.
    for script in ('check','build'):
        checked = subprocess.run(['npm','run',script],cwd=target,capture_output=True,text=True,timeout=30)
        assert checked.returncode == 0, checked.stdout + checked.stderr
    setup_logs = temporary / 'state/workspace-manager/setup-logs'
    node_logs = setup_logs / saved['id']
    completed = [json.loads(path.read_text())['data'] for path in node_logs.glob('*.json')]
    assert len(completed) == 2 and all(log['result']['succeeded'] for log in completed)
    assert all('executable' not in log and 'arguments' not in log for log in completed)
    # Simulate a stopped app that recorded an attempt but lost its final result.
    unfinished = dict(id=secrets.token_hex(16),projectId=saved['id'],kind='environment',
                      label='Incomplete test attempt',recordedAt=completed[0]['recordedAt'])
    unfinished_path = node_logs / (unfinished['id'] + '.json')
    unfinished_path.write_text(json.dumps(dict(schemaVersion=1,kind='setup-log',data=unfinished)))
    unfinished_path.chmod(0o600)
    history_before = {path:path.read_bytes() for path in node_logs.glob('*.json')}
    python = parent / 'setup-python'
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('h1#projects-title')")
        app.js("[...document.querySelectorAll('button')].find(button=>button.textContent === 'Setup history setup-node').click(); return true;")
        app.until("document.querySelector('[aria-label=\"Initialize Git history\"]')")
        assert app.js("return document.querySelector('[aria-label=\"Install dependencies history\"]').textContent.includes('Succeeded');")
        assert app.js("return document.querySelector('[aria-label=\"Incomplete test attempt history\"]').textContent.includes('No final outcome was saved');")
        assert all(path.read_bytes() == data for path,data in history_before.items())
        assert {path.name for path in node_logs.glob('*.json')} == {path.name for path in history_before}
        app.narrow_viewport()
        app.assert_layout()
        button(app,'Done')
        app.until("!document.querySelector('dialog')")
        create(app,'python','setup-python')
        button(app,'Review virtual environment')
        app.until("document.querySelector('[aria-label=\"Setup command review\"]')")
        assert not (python / '.venv').exists()
        versions(app)
        button(app,'Run reviewed setup')
        app.until("document.querySelector('[aria-label=\"environment setup result\"]')?.textContent.includes('completed')",timeout=30)
        assert (python / '.venv/pyvenv.cfg').is_file()
        assert '/proc/' not in (python / '.venv/pyvenv.cfg').read_text()
        button(app,'Done')
        app.until("!document.querySelector('dialog')")
        app.close()
    checked = subprocess.run([str(python / '.venv/bin/python'),'-I','-B','-c','import sys;print(sys.prefix)'],capture_output=True,text=True,check=True,timeout=10)
    assert checked.stdout.strip() == str(python / '.venv')
    assert all(not json.loads(path.read_text())['data']['commands'][0].get('approval') for path in records.glob('*.json'))
    print('PASS: native persistent setup history after restart, read-only unfinished outcomes, explicit version checks, separate Git/npm/venv reviews and execution, main branch without commits, dependency download with scripts disabled, bounded output/narrow layout, usable installed compiler/venv and unchanged startup approval.')


def check_recovery(temporary):
    parent = temporary / 'recovery-projects'
    parent.mkdir()
    target = parent / 'recovery-go'
    def button(app, text, region='dialog'):
        app.js("[...document.querySelectorAll(" + json.dumps(region + ' button') + ")].find(button=>button.textContent === " + json.dumps(text) + ").click();return true;")
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('h1#projects-title')")
        button(app,'New project','body')
        app.until("document.querySelector('#creation-name')?.matches(':enabled')")
        app.fill('#creation-name','recovery-go')
        app.fill('#creation-parent',str(parent))
        app.fill('#creation-module','example.com/recovery')
        app.click('dialog button[type=submit]')
        app.until("document.querySelector('section[aria-label=\"Generated files\"]')")
        button(app,'Confirm and create project')
        app.until("document.querySelector('ol[aria-label=\"Creation activity\"]')")
        button(app,'Done')
        app.until("!document.querySelector('dialog')")
        app.close()
    registry = temporary / 'config/workspace-manager/projects'
    registration = next(registry.glob('*.json'))
    original = json.loads(registration.read_text())['data']
    source = {str(path.relative_to(target)):path.read_bytes() for path in target.rglob('*') if path.is_file()}
    history_dir = temporary / 'state/workspace-manager/creation-history'
    log_file = next(history_dir.glob('*.json'))
    log = json.loads(log_file.read_text())
    log['data'].pop('result',None)
    log['data'].pop('finishedAt',None)
    log_file.write_text(json.dumps(log))
    # Model a crash after durable publication but before registration/final logging.
    # All removed metadata belongs exclusively to this temporary test fixture.
    registration.rename(temporary / 'original-registration.json')
    history_before = log_file.read_bytes()
    region = '[aria-label="recovery-go publication history"]'
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('h1#projects-title')")
        button(app,'Creation history','body')
        app.until("document.querySelector('" + region + "')")
        button(app,'Check recovery',region)
        app.until("[...document.querySelectorAll('" + region + " button')].some(button=>button.textContent === 'Review registration recovery')")
        assert not registration.exists()
        button(app,'Review registration recovery',region)
        app.until("document.querySelector('[aria-label=\"Recovered registration review\"]')")
        assert app.js("return document.querySelector('[aria-label=\"Recovered registration review\"]').textContent.includes('unapproved');")
        button(app,'Cancel recovery review',region)
        assert not registration.exists()
        button(app,'Review registration recovery',region)
        app.until("document.querySelector('[aria-label=\"Recovered registration review\"]')")
        button(app,'Restore registration',region)
        app.until("document.querySelector('" + region + "').textContent.includes('Registration restored.')")
        restored = json.loads(registration.read_text())['data']
        assert restored['id'] == original['id'] and restored['source'] == 'bootstrapped'
        assert restored['commands'][0]['configuration'] == original['commands'][0]['configuration']
        assert not restored['commands'][0].get('approval')
        assert log_file.read_bytes() == history_before
        assert {str(path.relative_to(target)):path.read_bytes() for path in target.rglob('*') if path.is_file()} == source
        button(app,'Check recovery',region)
        app.until("document.querySelector('" + region + "').textContent.includes('The original registration is present.')")
        app.narrow_viewport()
        app.assert_layout()
        button(app,'Done')
        app.until("!document.querySelector('dialog')")
        assert app.js("return !!document.querySelector('article[aria-label=\"Registered project recovery-go\"]');")
        app.close()
    # Later edits block restoration even when the original registration is absent.
    registration.rename(temporary / 'restored-registration.json')
    edited = (target / 'main.go').read_bytes() + b'\n// edited test fixture\n'
    (target / 'main.go').write_bytes(edited)
    with Native(temporary) as app:
        app.navigate('projects')
        app.until("document.querySelector('h1#projects-title')")
        button(app,'Creation history','body')
        app.until("document.querySelector('" + region + "')")
        button(app,'Check recovery',region)
        app.until("document.querySelector('" + region + "').textContent.includes('could not be verified as the reviewed starter')")
        assert not app.js("return [...document.querySelectorAll('" + region + " button')].some(button=>button.textContent === 'Review registration recovery');")
        assert not registration.exists() and (target / 'main.go').read_bytes() == edited
        app.close()
    print('PASS: native restart reconciliation, fresh/cancellable registration review, original ID/configuration restored unapproved, unchanged source/history, missing-registration refresh and refusal after source edits; no commands retried.')


def check_login(temporary):
    for mode in ['fixed', 'restore', 'hybrid']:
        root = temporary / mode
        root.mkdir(mode=0o700)
        env = dict(os.environ, XDG_CONFIG_HOME=str(root/'config'), XDG_STATE_HOME=str(root/'state'),
                   XDG_CACHE_HOME=str(root/'cache'), XDG_DATA_HOME=str(root/'data'))
        now = '2026-11-03T00:00:00Z'
        workspace_id, session_id = 'a'*32, 'b'*32
        desktop_id = 'org.example.WorkspaceOwnedLogin.desktop'
        captured = root/'handoffs'
        helper = root/'owned-login.py'
        helper.write_text('import pathlib, sys\nwith pathlib.Path(sys.argv[1]).open("a") as output: output.write("handoff\\n")\n')
        apps = root/'data/applications'
        apps.mkdir(parents=True)
        (apps/desktop_id).write_text('[Desktop Entry]\nType=Application\nName=Owned login fixture\nExec=/usr/bin/python3 '+str(helper)+' '+str(captured)+'\nTerminal=false\n')
        choice = dict(desktopId=desktop_id, duplicatePolicy='skip_if_running')
        workspace = dict(id=workspace_id, name='Owned login', description='', projectIds=[], applications=[choice], createdAt=now, updatedAt=now)
        session = dict(id=session_id, name='Owned restore', workspaceId=workspace_id, capturedAt=now, projects=[], applications=[choice], captureWarnings=[])
        for directory, identifier, kind, value in [(root/'config/workspace-manager/profiles',workspace_id,'workspace',workspace), (root/'state/workspace-manager/sessions',session_id,'session',session)]:
            directory.mkdir(parents=True, mode=0o700)
            record = directory/(identifier+'.json')
            record.write_text(json.dumps(dict(schemaVersion=4, kind=kind, data=value)))
            record.chmod(0o600)
        for directory in root.rglob('*'):
            if directory.is_dir():
                directory.chmod(0o700)
        binary = str(ROOT/'bin/workspace-manager-smoke')
        try:
            result = subprocess.run([binary, '--status'], env=env, capture_output=True, text=True, timeout=15)
            assert result.returncode == 0, result.stderr
            state = json.loads(result.stdout)
            settings = dict(state['settings'], autostart=True, startupMode=mode, defaultWorkspaceId=workspace_id, restoreSessionId=session_id)
            review = controller_rpc(root, dict(action='review_login', settings=settings, revision=state['settingsRevision']))
            assert not review['code'] and len(review['plan']['tasks']) == 1, review
            assert review['plan']['tasks'][0]['application']['desktopId'] == desktop_id
            enabled = controller_rpc(root, dict(action='commit', token=review['plan']['token']))
            assert not enabled['code'] and not captured.exists(), enabled
            subprocess.run([binary, '--stop-controller'], env=env, check=True, capture_output=True, timeout=15)
            deadline = time.monotonic()+8
            while (root/'state/workspace-manager/runtime/controller/control.sock').exists() and time.monotonic()<deadline:
                time.sleep(.05)
            for attempt in range(2):
                subprocess.run([binary, '--login'], env=env, check=True, capture_output=True, timeout=15)
                deadline = time.monotonic()+10
                while time.monotonic()<deadline:
                    if captured.exists() and not controller_rpc(root,dict(action='state'))['runtime']['busy']:
                        break
                    time.sleep(.05)
                assert captured.read_text() == 'handoff\n', 'login failed or replayed: '+mode
            review = controller_rpc(root,dict(action='review_session',id=session_id))
            assert len(review['plan']['tasks']) == 1 and review['plan']['tasks'][0]['application']['desktopId'] == desktop_id
            restored = controller_rpc(root,dict(action='commit',token=review['plan']['token'],switchPolicy='keep'))
            assert not restored['code'],restored
            deadline = time.monotonic()+10
            while time.monotonic()<deadline and captured.read_text() != 'handoff\nhandoff\n':
                time.sleep(.05)
            assert captured.read_text() == 'handoff\nhandoff\n', mode+' restore did not launch its captured desktop application'
        finally:
            subprocess.run([binary, '--stop-controller'],env=env,capture_output=True,timeout=15)
        print('PASS: '+mode+' native login after controller restart, non-D-Bus desktop handoff, replay prevention and session restore; only the owned helper ran.')


def check_ui(temporary):
    with Native(temporary) as app:
        app.navigate('workspaces')
        app.until("document.querySelector('.workspace-overview')")
        app.until("document.querySelector('.app-brand img').complete && document.querySelector('.app-brand img').naturalWidth > 0")
        app.assert_layout()
        assert app.js("return document.querySelectorAll('nav[aria-label=\"Main navigation\"] button').length;") == 3
        app.click('[aria-label=\"Save & Shutdown\"]')
        app.until("document.querySelector('dialog')?.textContent.includes('Confirm Save & Shutdown')")
        app.click('dialog .dialog-actions .secondary-button')
        app.until("!document.querySelector('dialog')")
        app.navigate('workspaces')
        app.click('[data-new-workspace]')
        app.until("document.querySelector('dialog')?.textContent.includes('New workspace')")
        app.until("document.activeElement?.id === 'workspace-name'")
        app.click('dialog [aria-label="Close dialog"]')
        app.until("!document.querySelector('dialog')")
        app.click('[aria-label="Quick actions"]')
        app.until("document.activeElement?.getAttribute('aria-label') === 'Search actions'")
        app.fill('[aria-label="Search actions"]', 'create project')
        app.click('.command-action')
        app.until("document.querySelector('dialog')?.textContent.includes('New project')")
        app.until("document.querySelector('#creation-name')?.disabled === false")
        app.click('dialog [aria-label="Close dialog"]')
        app.until("!document.querySelector('dialog')")
        app.click('[aria-label="Save session"]')
        app.until("document.querySelector('dialog input')?.value.startsWith('Session · ')")
        assert not controller_rpc(temporary, {'action': 'state'}).get('sessions')
        app.submit()
        app.until("!document.querySelector('dialog')")
        assert len(controller_rpc(temporary, {'action': 'state'})['sessions']) == 1
        for page in ['workspaces', 'projects', 'sessions', 'activity', 'settings', 'applications', 'diagnostics']:
            app.navigate(page)
            app.until("document.querySelector('main h1')")
            app.assert_layout()
        app.navigate('workspaces')
        app.until("document.querySelector('.workspace-overview')")
        app.js("localStorage.setItem('workspace-manager-appearance', 'light'); window.dispatchEvent(new Event('workspace-manager-appearance')); return true;")
        app.until("document.documentElement.dataset.theme === 'light'")
        app.assert_layout()
        encoded = app.tool('call_bound_method', name='main.ScreenshotService.Capture', args=[])
        Path('/tmp/workspace-manager-light.png').write_bytes(base64.b64decode(encoded))
        app.narrow_viewport()
        for page in ['workspaces', 'projects', 'sessions', 'activity', 'settings', 'applications', 'diagnostics']:
            app.navigate(page)
            app.until("document.querySelector('main h1')")
            app.assert_layout()
        app.navigate('workspaces')
        app.click('[data-new-workspace]')
        app.until("document.querySelector('dialog')?.textContent.includes('New workspace')")
        app.assert_layout()
        encoded = app.tool('call_bound_method', name='main.ScreenshotService.Capture', args=[])
        Path('/tmp/workspace-manager-narrow.png').write_bytes(base64.b64decode(encoded))
        app.click('dialog [aria-label="Close dialog"]')
        app.close()
    print('PASS: native global creation, palette search/focus, suggested session capture with explicit save, three primary destinations, supporting tools and narrow layouts, light theme and app icon.')


def check_lifecycle(temporary):
    project_id, command_id = 'a' * 32, 'b' * 32
    source = temporary / 'owned-server'
    source.mkdir()
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        port = probe.getsockname()[1]
    helper = source / 'owned_server.py'
    helper.write_text('from http.server import HTTPServer, BaseHTTPRequestHandler\nimport sys\nclass Handler(BaseHTTPRequestHandler):\n def do_GET(self):\n  self.send_response(200); self.end_headers(); self.wfile.write(b"owned-fixture")\nprint("owned-worker-ready", flush=True)\nHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()\n')
    configuration = dict(name='Owned server', executable=str(Path('/usr/bin/python3').resolve()),
                         arguments=['-u', str(helper), str(port)], workingDirectory=str(source),
                         runtime=dict(readiness=dict(kind='http', url=f'http://127.0.0.1:{port}/health', expectedStatus=200, timeoutSeconds=10, intervalSeconds=1), stop=dict(signal='SIGTERM', graceSeconds=3)))
    # Go's fingerprint normalizes empty slices; this fixture has only literal,
    # nonempty argv and no optional zero-valued runtime fields.
    fingerprint = hashlib.sha256(json.dumps(configuration, separators=(',', ':'), ensure_ascii=False).encode()).hexdigest()
    now = '2026-11-03T00:00:00Z'
    project = dict(id=project_id, name='Owned server', path=str(source), source='imported',
                   commands=[dict(id=command_id, configuration=configuration, approval=dict(fingerprint=fingerprint, approvedAt=now))], createdAt=now, updatedAt=now)
    registry = temporary / 'config/workspace-manager/projects'
    registry.mkdir(parents=True, mode=0o700)
    (temporary / 'config/workspace-manager').chmod(0o700)
    record = registry / f'{project_id}.json'
    record.write_text(json.dumps(dict(schemaVersion=3, kind='project', data=project)))
    record.chmod(0o600)
    workspace_id = 'c' * 32
    profiles = temporary / 'config/workspace-manager/profiles'
    profiles.mkdir(mode=0o700)
    profile = dict(id=workspace_id, name='Coding', description='A local development workspace', projectIds=[project_id], applications=[], createdAt=now, updatedAt=now)
    profile_file = profiles / f'{workspace_id}.json'
    profile_file.write_text(json.dumps(dict(schemaVersion=4, kind='workspace', data=profile)))
    profile_file.chmod(0o600)
    original = helper.read_bytes()
    run_id = None
    try:
        with Native(temporary) as app:
            app.navigate('projects')
            app.until("document.querySelector('[data-launch-id=\"" + project_id + "\"]')")
            app.click('[data-launch-id="' + project_id + '"]')
            app.until("document.querySelector('dialog')?.textContent.includes('Owned server')")
            assert str(helper) in app.js("return document.querySelector('dialog').innerText;")
            assert not list((temporary / 'state/workspace-manager/runtime/runs').glob('*.json'))
            app.click('dialog .dialog-actions .primary-button')
            app.until("!document.querySelector('dialog')")
            app.navigate('activity')
            app.until("document.querySelector('.runtime-run .task-state')?.textContent === 'ready'", timeout=20)
            app.until("document.querySelector('.runtime-run > .runtime-output')?.textContent.includes('owned-worker-ready')")
            app.navigate('workspaces')
            app.until("document.querySelector('[data-launch-id=\"" + workspace_id + "\"]')")
            app.click('[data-launch-id="' + workspace_id + '"]')
            app.until("document.querySelector('dialog')?.textContent.includes('Review Coding')")
            app.click('dialog .dialog-actions .primary-button')
            app.until("!document.querySelector('dialog')")
            app.navigate('activity')
            app.until("document.querySelector('.runtime-run .task-state')?.textContent === 'ready'")
            app.assert_layout()
            app.screenshot('activity')
            state = controller_rpc(temporary, {'action': 'state'})
            assert len(state['runtime']['runs']) == 1
            assert state['runtime']['activeWorkspaceId'] == workspace_id
            run_id = state['runtime']['runs'][0]['id']
            app.close()
        # The owned worker and HTTP server survive closing the GUI and killing
        # only this fixture's controller. No process-name matching is used.
        directory = os.open(temporary / 'state/workspace-manager/runtime/controller', os.O_RDONLY | os.O_DIRECTORY)
        try:
            with socket.socket(socket.AF_UNIX) as connection:
                connection.connect(f'/proc/self/fd/{directory}/control.sock')
                pid, uid, _ = struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))
                assert uid == os.getuid()
                assert b'--controller' in Path(f'/proc/{pid}/cmdline').read_bytes().split(b'\0')
                controller_pidfd = os.pidfd_open(pid)
                try:
                    signal.pidfd_send_signal(controller_pidfd, signal.SIGKILL)
                finally:
                    os.close(controller_pidfd)
        finally:
            os.close(directory)
        with urllib.request.urlopen(f'http://127.0.0.1:{port}/health') as response:
            assert response.read() == b'owned-fixture'
        with Native(temporary) as app:
            app.navigate('activity')
            app.until("document.querySelector('.runtime-run .task-state')?.textContent === 'ready'", timeout=20)
            assert len(controller_rpc(temporary, {'action': 'state'})['runtime']['runs']) == 1
            app.navigate('workspaces')
            app.until("document.querySelector('[data-launch-id]')")
            app.more('Coding')
            app.js("[...document.querySelectorAll('details[open] button')].find(button => button.textContent === 'Launch preferences').click(); return true;")
            app.until("document.querySelector('dialog')?.textContent.includes('Browser URLs')")
            app.assert_layout()
            app.click('dialog [aria-label=\"Close dialog\"]')
            app.until("!document.querySelector('dialog')")
            app.screenshot('dashboard')
            app.navigate('sessions')
            app.until("document.querySelector('.runtime-actions .primary-button')")
            app.click('.runtime-actions .primary-button')
            app.until("document.querySelector('dialog input')")
            app.fill('dialog input', 'Owned snapshot')
            app.submit()
            app.until("document.body.textContent.includes('Owned snapshot')")
            app.screenshot('sessions')
            first = controller_rpc(temporary, {'action': 'state'})['sessions'][0]
            assert first['value']['projects'][0]['id'] == project_id
            branch = controller_rpc(temporary, {'action': 'branch', 'id': first['value']['id'], 'name': 'Independent branch'})
            assert branch['session']['value']['parentSessionId'] == first['value']['id']
            assert len(controller_rpc(temporary, {'action': 'state'})['sessions']) == 2
            app.navigate('sessions')
            app.until("[...document.querySelectorAll('button')].some(b=>b.textContent.includes('Save & Shutdown'))")
            app.js("[...document.querySelectorAll('button')].find(b=>b.textContent.includes('Save & Shutdown')).click(); return true;")
            app.until("document.querySelector('dialog')?.textContent.includes('Confirm Save & Shutdown')")
            app.assert_layout()
            app.click('dialog .dialog-actions .secondary-button')
            app.until("!document.querySelector('dialog')")
            assert controller_rpc(temporary, {'action': 'state'})['runtime']['shutdownState'] == 'idle'
            app.navigate('activity')
            app.until("document.querySelector('.runtime-run')")
            app.click('.runtime-run .runtime-actions .secondary-button')
            app.until("document.querySelector('.runtime-run .task-state')?.textContent === 'stopped'", timeout=15)
            app.narrow_viewport()
            app.assert_layout()
            assert helper.read_bytes() == original
            app.close()
    finally:
        if run_id:
            try:
                controller_rpc(temporary, {'action': 'stop', 'id': run_id, 'force': True})
            except (OSError, ValueError):
                pass
    print('PASS: native owned server readiness/output, GUI close, controller SIGKILL/reconnect, preserved worker identity, session capture/branch, shutdown cancel, graceful stop and narrow layout; no power-off or browser handoff.')


if __name__ == "__main__":
    main()
