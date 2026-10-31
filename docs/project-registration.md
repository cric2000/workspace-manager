# Reviewed project registration

Phase 5, unit 2 adds canonical project registration and persistent core startup
configuration. It starts no project process. Open **Projects**, browse or enter a
directory, and inspect it. Select a suggestion, then choose **Configure project**.
Without a selected suggestion, the editor starts with no commands; custom commands
can be added explicitly. Missing tools can be omitted so registration can still proceed.

The editor supports a project name and up to 16 ordered startup commands. Each
command has a name, executable/tool, literal argument rows and working directory.
An empty argument row means an empty argv entry. Spaces remain inside the row;
no shell quoting, expansion, interpolation or command-line splitting occurs.
Shells require an explicitly configured shell executable and its arguments.
Do not put secrets in command arguments; [unit 3](project-settings.md) adds
environment-file references and the remaining project settings.

Choose **Review configuration** to resolve tool names through PATH and normalize
executable symlinks to absolute paths. Review displays the exact executable,
arguments (JSON-quoted to make empty/whitespace values visible), working directory,
name and order. **Approve and save project** persists this configuration. Cancel
and Back save nothing. Registered projects reopen through **Edit project …**.
An existing registration's canonical project directory cannot be changed here.

## Domain and schema

Start with `internal/domain/command.go`. `CommandConfiguration` is data, not an
execution function. Arguments share the custom-application limits: 128 entries,
8,192 characters per entry, 32 KiB total. Paths are absolute and bounded to 4,096
bytes. `StartupCommand` adds a stable ID and optional approval. Approval stores
the UTC time and SHA-256 fingerprint of all configuration fields, including name
and argument order. Nil and empty argv normalize to the same fingerprint.
Validation rejects a mismatched approval; this is a consistency check, not a
signature against a local user editing their own files.

Project schema **2** added `commands`. Session schema **2** supported these embedded
project snapshots. `NewProjectCodec` and `NewSessionCodec` strictly decode the
exact schema-1 shapes, including nested legacy projects. They add no commands or
approval during migration and do not rewrite a file on read. The next explicit
project edit archives the exact previous bytes before writing the current schema. Workspace
schema remains 3 and settings schema remains 1. Project/session schema **3** now
adds [runtime and project preferences](project-settings.md), preserving legacy
approval when no new fields are configured. Session branching deep-copies
command arguments and approval pointers as well as project values.

## Service boundary and review lifecycle

`internal/projects/registry_linux.go` owns registration. The existing `Inspect`
method remains independent of storage. `ListProjects` lazily provisions private
project storage; visiting Projects can therefore create empty app-owned storage
directories and lock files. Only `CommitRegistration` creates or updates records.
Project source directories and files are never written by these methods.

The native chooser is injected from `main.go` using the pinned Wails v3 dialog
API. It selects directories, does not create them, and neither inspects nor saves
the result automatically. Dialog cancellation preserves the previous path. Only
one chooser opens per service. A cancelled WebView call cannot dismiss an already
open OS dialog; close that dialog normally.

For each registration review the service:

1. Resolves the chosen root through symlinks to an existing canonical directory
   below filesystem root. Command working directories must resolve to that root
   or a subdirectory; executable paths may be outside the project.
2. Validates the draft and existing revision, assigns new project/command IDs on
   the server, and retains only command IDs belonging to this project.
3. Resolves each executable without running it and checks that it is a regular,
   executable file. Tools, scripts, dependencies and versions are never invoked.
4. Checks canonical-path uniqueness under a private feature operation lock shared
   by cooperating GUI processes, then holds the normalized plan under an opaque,
   five-minute review token. At most 64 unexpired reviews/outcomes are retained.
5. On approval, rechecks uniqueness, project revision, directory identities and
   executable identities/metadata before committing through the repository.

Commit accepts only the token, never replacement configuration. Returned Go values
are independent copies of the held plan/result. Repeated tokens return the cached
outcome without another write. Outcomes expire with their token and do not survive
application exit. Expired, changed or unsuccessful reviews require a fresh review.
If a save response is lost, **Check save outcome** uses the same token. Back/edit
is disabled during that uncertainty; close and reload if the token is no longer
available. A committed durability/refresh error requires reconciliation through
the registered-project list, never a blind second registration.

Directory device/inode/type/mode protect against replacement while ignoring normal
directory mtime changes. Executables also compare size, mtime and ctime. These are
point-in-time review checks; they neither freeze future code nor prevent a later
filesystem change. Approval covers configuration, not source/script/dependency
contents, dependency installation, successful startup or readiness. The later
execution engine must revalidate its own launch conditions.

## UI responsibilities and remaining work

`frontend/src/components/ProjectRegistry.tsx` owns saved-project loading and the
single edit/review dialog. The bridge wrappers live in `services/projects.ts`;
generated bindings are not hand edited. Stale edits preserve the draft for
reference and require reopening the latest record. An explicit inspector rescan
never replaces saved commands. Comparing new findings to approved configuration
is a later unit.

Environment references, readiness, preview URLs and editor/stop/delay preferences
are now implemented as configuration in [unit 3](project-settings.md).
Workspace association and metadata removal remain pending.
Execution, logs and process ownership remain Phase 7 work after the independent
controller is implemented. No project Run operation is exposed.

## Build, run and test

From the repository root, with dependencies installed:

```bash
export PATH="$PWD/.tools/bin:$PATH"
wails3 build
./bin/workspace-manager
go test -race ./...
go vet ./...
npm --prefix frontend test
go build -tags production,mcp -buildvcs=false -o bin/workspace-manager-smoke .
python3 scripts/native-smoke.py
```

For a sandbox with a read-only default Go cache, prefix Go/Wails commands with
`GOCACHE=/tmp/workspace-manager-go-build`. Native checks require an accessible
Wayland session; they use temporary XDG roots and test-owned application helpers.
They never execute project commands or shutdown. Native OS chooser interaction,
OS keyboard behavior, pixel/scaling review and X11 require manual acceptance.
