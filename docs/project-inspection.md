# Phase 5, unit 1: read-only project inspection

Open **Projects**, paste an absolute local directory path and choose **Inspect
directory**. The inspector reports language/framework evidence, package managers,
scripts or entrypoints, candidate commands, required tools and findings. Select a
candidate to inspect its tool, literal argument list, working directory and reason.
No candidate is automatically selected or treated as an approved command.

This unit does not register a project, save configuration, install dependencies or
run commands. It creates no project repository or project files. A native directory
chooser, persistent registration and reviewed command configuration belong to the
next units. The existing Project domain/storage schema remains at version 1.

## Code to follow

1. `frontend/src/pages/Projects.tsx` owns the path, current report and selected
   candidate. Editing the path clears the report. **Rescan directory** replaces
   only this transient report; the inspector has no approved configuration to
   overwrite. Cancel or navigation aborts the bridge request and discards late
   responses using a request generation.
2. `frontend/src/services/projects.ts` lazily imports the generated Wails binding
   and connects an AbortSignal to its CancellablePromise. It contains no command
   execution or persistence path.
3. `internal/projects/service.go` bounds inspection to two active requests and
   gives each an eight-second cooperative context budget. It returns safe error
   codes/messages without exposing parser or filesystem error text. It depends
   only on the `detection.Inspector` interface.
4. `internal/projects/detection/files_linux.go` resolves the selected root, pins
   its directory descriptor, and opens relative metadata using Linux `openat2`
   beneath that descriptor with symlink traversal disabled. The selected root
   itself may be a symlink; the report shows its canonical path. All subsequent
   file and directory opens remain confined to that root.
5. `inspect.go` combines independent detectors and their provenance/findings. It
   reports alternatives rather than selecting a startup plan. `exec.LookPath`
   inspects PATH presence only; it never invokes a tool or asks for its version.

File opens are read-only and nonblocking. The reader rejects FIFOs, devices,
symlinked files and intermediate symlink directories before reading content. It
fails closed if the kernel lacks the required `openat2` support. File contents must
be UTF-8 without NUL, except lockfiles used only as regular-file presence markers
(including binary `bun.lockb`). It does not load `.env` files, follow referenced
requirements files, import Python modules or evaluate Make includes. Reports contain
selected names/evidence rather than raw script or source bodies.

## What each detector establishes

| Metadata | Evidence and suggestions |
| --- | --- |
| `package.json` | Valid JSON, declared script names and framework dependencies; `dev`, `start`, `serve` and `run` scripts become candidates only with manager evidence |
| npm/pnpm/Yarn/Bun lockfiles and `packageManager` | Manager alternatives; conflicts are displayed and no manager is silently chosen |
| `go.mod` and bounded `.go` files | Parsed module metadata plus non-test `package main` files with a main function matching the inspector's host build constraints; each main package gets its own `go run` argument |
| `pyproject.toml` | Python dependencies and console entrypoints; an explicit `[tool.fastapi] entrypoint` yields a FastAPI CLI candidate |
| `requirements.txt` | Dependency-name evidence only; includes, editable installs and URLs are not followed |
| `manage.py` | A conventional Django command, explicitly labelled as unverified source/environment |
| `Cargo.toml` and conventional binary locations | Explicit/auto-discovered binary names, `autobins` and edition rules; required-feature targets are withheld until flags are chosen |
| Compose filename variants | Services from a simple YAML mapping and an explicit `docker compose --file … up` candidate per file; no merge/override relationship is assumed |
| `Makefile` | Simple explicit target names; startup-named targets yield suggestions without evaluating recipes, variables, includes or conditionals |

JavaScript/TypeScript parsing rejects duplicate keys, unsupported field types and
trailing JSON. Script bodies and dependency versions/URLs are not copied into the
report. Script names with unsupported characters are not proposed. Monorepo workspace
declarations warn that only the root manifest's scripts were inspected.

Go uses the pinned `golang.org/x/mod/modfile` parser and Go's AST/build-constraint
readers. It never runs `go list`, `go env`, compilation or module resolution. It
excludes tests, hidden/underscore paths, conventional generated/dependency directories
and nested modules. Multiple main packages require an explicit choice. Compilation,
dependencies, workspace context and startup behavior remain unverified.

Python metadata uses a TOML parser. It never imports source, probes a virtual
environment or guesses an ASGI object from source text. FastAPI without an explicit
entrypoint produces a finding. A declared console command must already be installed
in the intended environment. Optional/development dependency groups, environment
selection and source-level inference remain future work.

Cargo parses TOML and observes target paths without `cargo metadata` or build-script
execution. Explicit paths outside the selected root, unavailable paths and targets
requiring feature flags are not proposed. Workspace member inheritance is not
resolved. Binary source/buildability is not validated by a Rust compiler.

Compose parsing does not expand aliases, interpolate environment values, read
includes, validate the full Compose schema, inspect images or probe Docker plugins.
Complex/unsupported structures produce findings. Make target detection is explicitly
conventional: conditional/defined targets may not be runnable. It never uses
`make -n` or `make -p`, which would still evaluate Make expressions.

## Resource and trust limits

- 512 KiB per text file, 8 MiB of text per inspection, 256 text files.
- 2,048 directory entries total; Go descent stops after four directory levels.
- At most 64 candidates, 64 findings and 128 entrypoint names per stack.
- Recursive TOML/YAML parsing has a conservative lexical complexity precheck:
  delimiter depth 64, leading indentation 128, dots per line 128 and punctuation
  per line 1,024. Delimiters inside strings/comments count too, so unusually complex
  valid files may be refused rather than parsed. JSON has a token nesting limit 64.

Limits or unreadable/malformed metadata mark the result partial. Other independent
detectors can still return evidence. Results are a point-in-time observation, not
an atomic filesystem snapshot. The context is cooperative: an already pending
filesystem operation cannot necessarily be interrupted immediately, particularly
on unusual or remote filesystems. The GUI remains responsive and ignores stale
results after cancellation.

Suggestions are untrusted project metadata. Running package scripts, Make recipes,
Cargo builds or a Python entrypoint later can execute project code and hooks. A
future approval operation must resolve/review the executable, argv, working
directory and configuration; this report grants no execution authority. Project
execution remains gated on the independent controller in Phase 7.

## Checks and commands

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

Fixtures cover each manager, conflicting/malformed metadata, multiple Go mains,
build tags, nested modules, explicit FastAPI and console entrypoints, Cargo
auto-discovery/required features, Compose alternatives, static Make targets,
symlinks/FIFOs, UTF-8/NUL and resource limits. A fake executable on PATH plus project
script/Make sentinels verify that detection does not invoke tools or project code.
Service tests cover safe errors, concurrent slots and cancellation. Frontend tests
cover pending/duplicate requests, evidence/argv, safe errors, rescan, cancellation
and ignored navigation responses. Native observations are in [validation.md](validation.md).

The new parser dependencies are pinned: BurntSushi TOML 1.6.0, go.yaml.in/yaml/v3
3.0.4 and x/mod 0.41.0 (compatible with this repository's Go 1.26 baseline). No
project runtime or framework dependency is installed during inspection.

## Primary references

- [TOML parser](https://github.com/BurntSushi/toml/releases/tag/v1.6.0)
- [Go module parser](https://pkg.go.dev/golang.org/x/mod@v0.41.0/modfile)
- [Maintained Go YAML versions](https://github.com/yaml/go-yaml)
- [Cargo target discovery](https://doc.rust-lang.org/cargo/reference/cargo-targets.html)
- [FastAPI explicit entrypoints](https://fastapi.tiangolo.com/fastapi-cli/)

Next: reviewed registration and persistent command configuration with explicit
schema evolution, environment references, readiness/preview/editor/stop settings,
rescan comparison and tests proving that edits do not silently retain approval.
