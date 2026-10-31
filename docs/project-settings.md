# Project environment and startup settings

Phase 5, unit 3 adds reviewed, persistent settings to imported projects. Open a
registered project's editor (or configure an inspected directory). Preview/editor
settings apply to the project. Expand **Command … environment and startup behavior**
for settings that belong to one startup command. Review every change before saving.
Nothing is executed, opened or probed by this unit.

## What is saved

| Setting | Scope | Validation and meaning |
| --- | --- | --- |
| Environment files | Each startup command | Up to 16 distinct, ordered, clean paths relative to the project root; no raw environment values |
| Launch delay | Each startup command | Integer seconds, 0–3600; zero means no configured delay |
| Readiness | Each startup command | Optional HTTP status or TCP port on a loopback host |
| Readiness timing | Each check | Timeout 1–300 seconds; interval 1–30 seconds and no greater than timeout |
| HTTP readiness | Each check | Explicit HTTP(S) URL, expected status 200–599, no credentials or fragment; loopback host required |
| TCP readiness | Each check | `localhost` or a loopback IP, explicit port 1–65535 |
| Stop policy | Each startup command | Optional SIGTERM/SIGINT and 1–120 second grace period; no automatic force stop |
| Stop action | Each stop policy | Optional executable, literal argv and project-contained working directory |
| Preview URL | Project | Optional explicit HTTP(S) URL without credentials; no universal/default port |
| Editor action | Project | Optional executable, literal argv and project-contained working directory |

Editor setup initially includes the project directory as a visible argument. Edit
or remove it for the selected editor's CLI. Editor and stop actions accept either
a tool on PATH or an absolute executable path. Review resolves the executable and
shows all literal arguments, including empty arguments. They use the same argument
and path limits as startup commands. Stop action configuration is intended to run
before signalling the owned process group in the future controller; its grace
period bounds the graceful stop operation. An omitted policy does not silently
authorize a custom action or a force stop.

Changing readiness kind replaces its previous fields, so an HTTP URL/status cannot
survive unnoticed inside a TCP configuration. Removing a stop policy removes its
custom action. **Clear Command … runtime settings** removes that command's optional
environment/readiness/stop/delay configuration. Preview and editor can be cleared
independently. Saving any of these changes still requires a new review.

## Environment references without copying secrets

Only file paths are saved and included in reviews/session snapshots. This unit
does not parse `.env`, source shell scripts, expand variables, read referenced file
contents, or persist environment values. URL/argument fields are ordinary saved
metadata; use environment references rather than putting credentials in those fields.

`CommandRuntime.Validate` rejects absolute paths, `..` escapes, unclean/empty paths,
duplicates and excessive counts. The project service uses Linux `openat2` with
`O_PATH`, `RESOLVE_BENEATH`, `RESOLVE_NO_SYMLINKS` and `RESOLVE_NO_MAGICLINKS` to
inspect existing references beneath the canonical project root. It rejects symlink
components, directories, devices and FIFOs without reading their data or blocking
on FIFO I/O. Missing paths can be saved for later creation; other access failures
are reported safely. Review and commit both repeat this check.

Approval covers reference paths and order, not future contents. Normal edits to an
environment file do not invalidate approval of its path. The future controller
must recheck containment/file type when loading references, bound parsing, avoid
secret values in diagnostics and define environment merging/format support before
execution is implemented. The editor action has no environment-file configuration.

## Domain, approval and migrations

Study `internal/domain/project_settings.go` first. `CommandRuntime` belongs to
`CommandConfiguration`, so startup approval fingerprints now also cover reference
order, delay, readiness fields, stop signal/grace and the complete stop action.
`ProjectPreferences` holds the preview URL and editor action, with its own approval
fingerprint/time. Preview/editor changes cannot retain approval for old preferences.
The registration draft excludes approval fields; only the service grants approval
after the user confirms the held review token.

Editor and stop executables/working directories are normalized and checked just
like startup commands. The review records their metadata identities and rejects
replacement/change before saving. Returned review/outcome values, saved project
drafts and session branches deep-copy nested arrays, readiness, stop actions,
editor actions and approval pointers.

Projects and sessions now use schema **3**. Schema 2's exact project/command shape
is decoded explicitly, including project values embedded in old sessions. New
`runtime` or `preferences` fields in old envelopes are rejected. Optional new fields
remain absent during migration. Their omission preserves the exact historical
startup fingerprint algorithm, so existing valid schema-2 approvals remain valid
without inventing any new approval or settings. Schema 1 migrates through schema 2.
No read rewrites the source; the next explicit project edit archives exact old bytes.
Workspace schema remains 3 and settings schema remains 1.

## UI, tests and remaining work

`frontend/src/components/ProjectSettings.tsx` contains the settings fields and review
renderers; `ProjectRegistry.tsx` owns the edit/review/save lifecycle. The service
normalizes/validates inputs and generated Wails bindings carry the typed data.
Frontend state keeps preference approval out of drafts and clones nested settings
when reopening a saved record. Review displays the actual values; it does not
derive a preview port or replace saved settings from inspection results.

No HTTP/TCP request, editor/stop action, startup command, environment-file load or
launch delay runs in this unit. The Phase 7 controller will implement those runtime
behaviors and revalidate executable/path/endpoint conditions. In particular, a
loopback readiness implementation must check resolved addresses and redirects and
avoid proxying probes outside loopback. A readiness result will never be inferred
from merely saving its configuration. Source/dependency changes remain outside
configuration approval.

Phase 5 still needs rescan comparison, workspace association and the complete import
workflow gate. Metadata deletion and richer detector cases are not silently claimed.

From the repository root:

```bash
export PATH="$PWD/.tools/bin:$PATH"
wails3 build
./bin/workspace-manager
go test -race ./...
go vet ./...
npm --prefix frontend test
go build -tags production,mcp -buildvcs=false -o bin/workspace-manager-smoke .
python3 scripts/native-smoke.py
# Focused project-only workflow (two GUI instances):
python3 scripts/native-smoke.py --projects-only
```

Use `GOCACHE=/tmp/workspace-manager-go-build` on Go/Wails commands if the default
cache is read-only. Native tests require the graphical session and isolated IPC
fixtures require access to their normal local facilities. The native test binary
uses a loopback, token-protected MCP endpoint; the production binary does not.
See [validation.md](validation.md) for observed checks and manual limitations.
