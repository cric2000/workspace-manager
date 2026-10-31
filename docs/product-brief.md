# Build a Linux Developer Workspace Manager — End-to-End V1 Implementation

Act as a principal Go engineer, senior React/TypeScript engineer, Linux desktop specialist, and software architect.

Help me build a complete, polished, open-source Linux desktop application from scratch.

I want to develop it step by step, understand its architecture, and implement reliable production-quality code.

The application will manage the complete lifecycle of a developer workspace:

**Create or restore workspace → Open or create projects → Initialize and run → Preview → Save session → Safely shut down → Resume later.**

Do not create a toy application. However, keep the V1 architecture practical, maintainable and appropriately scoped.

Do not dump the entire codebase at once. We will implement it incrementally, file by file or in small, testable units.

---

# PART 1 — PRODUCT DEFINITION

## 1. Project overview

Build a Linux desktop utility tentatively called **Workspace Manager**.

Its purpose is to eliminate repetitive developer setup tasks and provide a central place to create, launch, save and restore development environments.

A developer should be able to:

1. Start Ubuntu.
2. Automatically launch a selected workspace.
3. Restore supported applications and projects from an earlier session.
4. Create a new workspace without affecting an existing one.
5. Import an existing software project.
6. Detect how that project should run.
7. Create a new project from a framework/language template.
8. Automatically initialize its files and configuration.
9. Install dependencies when required and explicitly approved.
10. Open the project in a selected editor.
11. Run its initial development command.
12. Preview the generated application.
13. Add the project to an existing or new workspace.
14. Save the current workspace as a session.
15. Create a new session from an older session.
16. Save the current session before shutdown.
17. Gracefully stop application-managed processes.
18. Shut down Ubuntu normally.
19. Restore the session after the next login.

All functionality should work locally without cloud infrastructure.

## 2. Target platform

Initial support:

- Ubuntu 26.04.1 LTS
- GNOME desktop
- Wayland as the primary display protocol
- X11 where reasonably possible
- Linux AMD64
- Single-user desktop installation

Do not implement Windows or macOS support in V1.

Design Linux integration behind interfaces so future platform support remains possible.

## 3. Technology stack

Use:

- Go for application logic
- Wails v3 for the desktop application
- React
- TypeScript
- Tailwind CSS
- shadcn/ui where compatible
- Lucide icons
- JSON for local persistence
- XDG Base Directory specification
- XDG Autostart
- Linux process APIs
- D-Bus for supported desktop/system integration
- Go testing
- Vitest for frontend tests

Do not introduce:

- PostgreSQL
- Redis
- Cloud infrastructure
- Authentication services
- SaaS APIs
- AI-based project generation
- Docker as an application runtime requirement

The projects managed by our application may use Docker, but Workspace Manager itself must not require Docker.

Use stable dependency releases and verify their current APIs before writing integration code.

---

# PART 2 — CORE DOMAIN MODEL

## 4. Separate workspace, session and project

These are different concepts and must remain distinct.

### Workspace

A reusable configuration describing a development environment.

Contains:

- ID
- Name
- Description
- Applications
- Project references
- Browser configurations
- Startup actions
- Startup mode preferences
- Launch order
- Delays
- Default editor
- Created and updated timestamps

Examples:

- Coding
- Job Search
- Learning
- Backend Development
- Personal

A workspace is reusable and editable.

### Session

A snapshot of a particular working environment at a particular time.

Contains:

- Session ID
- Name
- Timestamp
- Associated workspace ID, if applicable
- Optional parent session ID
- Recognized applications
- Known project configurations
- Supported browser restore metadata
- Managed startup tasks
- Restore metadata
- Capture warnings

Sessions represent saved states rather than workspace templates.

Sessions must have stable IDs.

A newly saved session should be immutable by default.

Updating an existing session must be an explicit operation, with recoverable history or a backup.

### Project

An individual software codebase.

Contains:

- Project ID
- Name
- Absolute directory path
- Source type: imported or bootstrapped
- Language/framework metadata
- Preferred editor
- Approved startup commands
- Readiness checks
- Preview URL
- Optional environment configuration references
- Project-specific startup settings

Projects can belong to multiple workspace profiles through references.

Do not duplicate the entire project record unnecessarily.

### Active workspace runtime

Keep a separate runtime model recording:

- Active workspace ID
- Active session origin
- Running launch tasks
- Managed processes
- Project readiness
- Application launch results
- Current operation status

Do not mix transient process IDs into the permanent workspace profile model.

---

# PART 3 — WORKSPACE MANAGEMENT

## 5. Workspace profiles

Users can:

- Create a workspace
- Rename a workspace
- Edit its applications
- Add or remove projects
- Duplicate a workspace
- Delete a workspace
- Set a default workspace
- Launch a workspace manually
- Switch between workspaces
- Save workspace changes

Deleting a workspace must not delete project directories.

When switching workspaces, allow:

1. Keep current applications running.
2. Stop managed processes belonging only to the previous workspace.
3. Cancel the switch.

Default to preserving unrelated applications and external processes.

A workspace may contain multiple projects.

## 6. Startup modes

Support three startup modes.

### Fixed Workspace

Launch the configured applications, projects and commands from the selected profile.

### Restore Previous Session

Load the latest selected valid session snapshot.

Restore supported applications and projects.

Use native browser or editor recovery capabilities where applicable.

Do not promise exact restoration of arbitrary unsaved application state.

### Hybrid Mode

Load a saved session and then launch missing components from the default workspace.

Deduplicate applications, projects and startup tasks by stable identifiers or configured instance identity.

Hybrid mode must not blindly start duplicate servers.

The startup mode should be configurable in Settings.

---

# PART 4 — SESSION MANAGEMENT

## 7. Save sessions

Allow the user to save a session at any point.

Session capture should record:

- Recognized running applications
- Application launch metadata where available
- Workspace association
- Managed projects
- Known project working directories
- Startup configurations
- Browser recovery preferences
- Relevant restore settings

Capture recognized applications launched outside Workspace Manager where supported by desktop APIs or explicit application adapters.

For unsupported applications, show a warning or allow the user to manually map them to a launch command.

Do not assume arbitrary GUI window enumeration works on GNOME Wayland.

Session snapshots must not contain secrets, raw environment files or captured application memory.

Use atomic persistence.

Preserve the last known good snapshot if a save fails.

## 8. Restore sessions

Provide a Sessions page.

Users should be able to:

- View saved sessions
- See when each was created
- View applications and projects inside
- Restore a session
- Rename a session
- Delete a session with confirmation
- Save a new session based on an old one

During restore:

1. Load and validate the snapshot.
2. Check required applications.
3. Check project directory availability.
4. Preview missing dependencies.
5. Start supported applications.
6. Launch approved project commands.
7. Display progress and failures.

Restoration must be best-effort and transparent.

Do not falsely claim that arbitrary unsaved editor content or terminal history has been restored.

## 9. Session branching

This is an essential V1 feature.

Example:

User restores Monday's Coding Session.

Then they:

- Open another project.
- Launch another editor.
- Add browser URLs.
- Start a new development server.

The user can select:

**Save as New Session**

This creates a new session with its own ID and an optional parent session reference.

The original session remains unchanged.

Also provide:

**Update Existing Session**

This requires explicit confirmation and preservation of recoverable history.

Users should be able to branch sessions multiple times.

Session branching should not duplicate physical project directories.

---

# PART 5 — APPLICATION DISCOVERY AND LAUNCHING

## 10. Detect installed applications

Discover installed Linux desktop applications using `.desktop` metadata.

Support standard user and system application directories.

Show:

- Application name
- Icon
- Launch entry
- Availability
- Application identifier where available

Provide a searchable application picker.

Support common apps:

- Chrome
- Brave
- VS Code
- Antigravity
- Terminal emulators
- Other installed applications

Also allow custom executable configuration.

Parse desktop entries according to the Desktop Entry specification.

Do not naively execute untrusted `Exec` strings using a shell.

Handle path quoting and desktop-entry field codes correctly.

## 11. Smart application launching

Before launching an application:

- Check its configured instance identity.
- Determine whether an appropriate instance already exists.
- Apply the user's duplicate-launch preference.
- Launch using the appropriate desktop mechanism.
- Track the launch result.

Do not assume that a successful launch command means the application is ready.

Support:

- Launch
- Skip if running
- Retry failed launch
- Show launch error

Do not terminate arbitrary existing desktop applications.

## 12. Browser management

Allow users to configure:

- Browser
- URLs
- Native session restoration preference
- Launch behavior

Support Chrome and Brave initially.

When restoring old sessions, rely on browser-native session recovery for previous tabs.

Do not manipulate browser session databases.

Avoid opening the same configured URLs repeatedly when practical.

Explain that exact tab restoration depends on browser capabilities and settings.

---

# PART 6 — PROJECT-AWARE STARTUP

## 13. Import an existing project

Users can choose an existing local directory.

The application analyzes project metadata to identify the stack and suggest startup commands.

Support:

| Project metadata | Detection |
|---|---|
| package.json | JavaScript/TypeScript |
| package-lock.json | npm |
| pnpm-lock.yaml | pnpm |
| yarn.lock | Yarn |
| bun.lock / bun.lockb | Bun |
| go.mod | Go |
| pyproject.toml | Python |
| requirements.txt | Python |
| manage.py | Django |
| Cargo.toml | Rust |
| compose.yaml | Docker Compose |
| docker-compose.yml | Docker Compose |
| Makefile | Make targets |

Use metadata parsing and filesystem inspection.

Do not execute project code during detection.

Detection should return:

- Detected language
- Framework, when identifiable
- Package manager
- Available scripts or entry points
- Candidate commands
- Required tools
- Explanation or confidence
- Ambiguity warnings

## 14. Automatic startup suggestions

Examples:

Next.js project with a `dev` script:

`npm run dev`

Go application with a runnable main package:

`go run .`

Python FastAPI application with a known entry point:

`uvicorn app.main:app --reload`

Docker Compose project:

`docker compose up`

These are examples, not universal defaults.

Use actual detected project information.

For Go projects with multiple main packages, suggest the matching command rather than assuming the repository root is runnable.

For Python, detect the correct entry point where possible.

If ambiguous, let the user choose or specify a custom command.

Never execute suggested commands without explicit approval.

## 15. Save approved project configuration

After approval, store:

- Startup command
- Arguments
- Working directory
- Environment references
- Readiness check
- Preview URL
- Editor
- Stop behavior
- Optional launch delay

Do not rescan and overwrite approved configurations on every startup.

Provide a Rescan Project action.

Show changes before applying them.

## 16. Multi-project workspaces

Support multiple projects in one workspace.

Example:

Backend Workspace:

- Go API
- Next.js frontend
- Docker Compose services

Each project can have multiple commands.

Allow configurable command order and readiness checks.

Keep dependencies simple for V1.

Avoid designing a large workflow orchestration language.

---

# PART 7 — NEW PROJECT BOOTSTRAPPER

## 17. Project creation wizard

Users must be able to create a new project directly from Workspace Manager.

Workflow:

1. Click New Project.
2. Enter project name.
3. Select a parent directory.
4. Choose language/framework.
5. Select starter configuration.
6. Choose initial options.
7. Preview files and commands.
8. Confirm initialization.
9. Generate project.
10. Optionally install dependencies.
11. Open the project in an editor.
12. Optionally run the project.
13. Preview or inspect its output.
14. Add the project to a workspace.
15. Save the workspace/session if desired.

Do not automatically close or modify another workspace.

## 18. Supported V1 templates

Implement five templates.

### Go Application

Generate:

- go.mod
- main.go
- .gitignore
- README.md

Options:

- Go module path
- Initialize Git
- Open in editor
- Run initial application

Use the installed Go toolchain.

Choose a default module path only when valid; otherwise request a module identifier.

### Node.js + TypeScript

Generate:

- package.json
- tsconfig.json
- src/index.ts
- .gitignore
- README.md

Provide working scripts for development and build.

Use a compatible package manager and runtime.

### Next.js

Use the official Next.js scaffolding tool.

Allow the user to select supported options such as:

- TypeScript
- Tailwind
- App Router
- Source directory
- Package manager

Determine supported flags from the installed or documented scaffolding version.

Do not hardcode obsolete CLI options.

### Python

Generate:

- pyproject.toml
- Starter Python package or entry point
- .gitignore
- README.md

Optionally create a virtual environment.

Choose a compatible Python executable.

### FastAPI

Generate:

- pyproject.toml
- FastAPI entry point
- Basic health endpoint
- README.md
- .gitignore

Provide a verified command for running the initial API application.

## 19. Shared project templates architecture

Implement a bootstrap template registry.

Each template must define:

- Template ID
- Display name
- Supported options
- Required executables
- Validation rules
- Files created
- Initialization commands
- Dependency installation method
- Suggested startup command
- Readiness check
- Preview behavior

Use small interfaces and reusable services.

Avoid five unrelated scaffolding systems.

Keep internal templates minimal.

Use official scaffolding tools for frameworks where appropriate.

## 20. Project initialization safety

The bootstrapper must:

- Validate names and paths.
- Refuse to overwrite existing populated directories.
- Avoid symlink-based path escapes.
- Show the intended files and commands.
- Require approval before executing external scaffolding tools.
- Display progress.
- Support cancellation.
- Detect missing prerequisites.
- Preserve logs.
- Avoid exposing secret values.
- Clean up only files proven to have been created by the current operation.

Prefer creating projects in a new temporary staging directory and moving them into place after successful initialization where feasible.

Avoid destroying user files if initialization fails.

Network-dependent templates should clearly explain when downloads are required.

---

# PART 8 — RUN & PREVIEW

## 21. Run newly created projects

After project creation, offer:

- Open in Editor
- Run Project
- Open Preview
- Add to Workspace
- Save Session

A user can select more than one action.

If the user chooses Run Project:

1. Validate required commands.
2. Use the saved startup configuration.
3. Start the application.
4. Monitor output.
5. Check readiness.
6. Report success or failure.

For long-running development servers, keep them running under managed process supervision.

For short-lived CLI applications, display output and exit code.

## 22. Preview behavior

For web apps:

- Wait for configured localhost readiness.
- Open the actual configured URL in the browser.
- Show the local endpoint and readiness status.

For APIs:

- Open a configured documentation or health endpoint if available.
- Report HTTP status.

For CLI projects:

- Show stdout, stderr and exit code.
- Do not invent a web preview.

Do not hardcode port 3000 as the universal project URL.

Allow users to edit preview URLs.

Handle port conflicts and startup failures.

---

# PART 9 — STARTUP ENGINE AND PROCESS MANAGEMENT

## 23. Startup orchestration

Create a reusable Go startup engine.

It must support:

- Application tasks
- Project editor tasks
- Project command tasks
- Browser URL tasks
- Delayed tasks
- Readiness checks
- Retry
- Cancellation
- Status updates

Task states:

- Pending
- Starting
- Running
- Ready
- Failed
- Skipped
- Stopping
- Stopped

Use context-aware concurrency.

Long-running servers should not block the UI.

One failed task should not crash the entire application.

## 24. Process ownership

Track processes started by Workspace Manager.

Store runtime information such as:

- Process ID
- Process start identity
- Project ID
- Workspace launch ID
- Command
- Working directory
- Start time
- Current state

Use process groups or equivalent Linux mechanisms when appropriate.

Prevent accidental duplicate development servers.

Do not assume two processes are identical simply because they have the same executable name.

Do not terminate processes that the application does not own.

Account for the controller restarting while child processes remain alive.

## 25. Graceful termination

When stopping a managed development server:

1. Identify the correct process/process group.
2. Send the appropriate termination signal.
3. Wait within a configured timeout.
4. Report the result.
5. Offer an explicit force-stop option where appropriate.

Do not kill browsers, editors, user terminals or unrelated applications by process name.

A command may also have its own configured safe stop action, such as an explicitly approved Docker Compose shutdown command.

---

# PART 10 — AUTOMATIC STARTUP

## 26. Ubuntu login autostart

Use XDG Autostart.

Users can:

- Enable autostart
- Disable autostart
- Select default workspace
- Select startup mode
- Configure an initial delay

Autostart must operate after user login.

Do not require root privileges.

Avoid duplicate autostart entries.

Respect XDG paths and handle paths containing spaces.

## 27. Background controller lifecycle

Design the application so that closing the GUI does not unexpectedly terminate managed project servers.

Prefer a single Go executable with separate CLI modes if practical.

Possible architecture:

- GUI mode
- Controller mode
- Login startup mode
- CLI management mode

Choose the simplest reliable arrangement after reviewing Wails lifecycle behavior.

Ensure only one active controller manages a given user session.

Use safe local IPC if the GUI and controller run separately.

Prevent concurrent state corruption.

Do not expose an unauthenticated network control API.

The GUI should be reopenable without launching duplicate controllers.

---

# PART 11 — SAVE & SHUTDOWN

## 28. Save & Shutdown workflow

Implement a user-initiated Save & Shutdown action.

This is a headline feature.

Workflow:

1. User clicks Save & Shutdown.
2. Display detected applications and managed projects.
3. Explain what can be restored.
4. Explain unsupported state.
5. Offer Save & Shutdown, Shutdown Without Saving and Cancel.
6. If saving, capture the session.
7. Persist and verify the snapshot.
8. Stop managed project services gracefully.
9. Coordinate with the normal desktop shutdown mechanism.
10. Request normal shutdown.
11. Handle cancellation, permission failures and errors.

Do not automatically kill unrelated desktop applications.

Allow GNOME and individual apps to handle their normal save/close dialogs.

## 29. Shutdown state machine

Implement explicit states:

- Idle
- Confirming
- Capturing
- SnapshotSaved
- StoppingManagedProcesses
- RequestingShutdown
- CompletedRequest
- Failed
- Cancelled

Completion means the shutdown request was successfully handed to the OS; do not claim the computer powered off merely because the API returned success.

Preserve the snapshot if the OS denies shutdown.

If snapshot persistence fails, do not proceed with Save & Shutdown unless the user explicitly approves an alternative.

## 30. Shutdown integration

Use supported GNOME/logind APIs.

Do not default to force shutdown commands.

Do not assume standard Ubuntu power-menu interception is always possible.

Do not use indefinite logout inhibitors.

Any temporary inhibitor must be released reliably, including on failure paths.

Integrating with the standard Ubuntu shutdown confirmation may be investigated later, but universal interception is outside V1.

---

# PART 12 — DESKTOP UI

## 31. Visual design

Build a polished developer-focused desktop interface.

Design principles:

- Clean and professional
- Minimal visual clutter
- Consistent spacing
- Light and dark themes
- Clear status indicators
- Responsive desktop layouts
- Keyboard accessibility
- Informative errors
- No unnecessary animation

Use React, TypeScript, Tailwind and suitable shadcn/ui components.

## 32. Navigation

Suggested application navigation:

**Dashboard**

Overview of active workspace, running projects, recent sessions and quick actions.

**Workspaces**

Create, edit, duplicate, launch and switch profiles.

**Projects**

Import existing projects or create new ones.

**Sessions**

Save, restore, branch, rename and manage snapshots.

**Activity**

Launch progress, running tasks, logs and errors.

**Settings**

Autostart, startup mode, default workspace, editor, appearance, session retention and shutdown behavior.

## 33. Dashboard

Show:

- Active workspace
- Current session origin
- Applications in workspace
- Active projects
- Managed development servers
- Readiness indicators
- Recent sessions
- Autostart status
- Errors

Main actions:

- Launch Workspace
- New Workspace
- New Project
- Restore Session
- Save Session
- Save & Shutdown

## 34. New Project Wizard

Use a guided interface with:

- Project name
- Parent directory
- Language/framework
- Template options
- Git initialization
- README generation
- Dependency installation
- Editor
- Run after creation
- Add to workspace

Include a final review screen showing commands and file operations.

During initialization, show progress and meaningful errors.

After completion, show Run & Preview actions.

## 35. Session manager interface

Display session cards or rows containing:

- Session name
- Workspace name
- Save timestamp
- Number of recognized applications
- Number of managed projects
- Parent session indicator
- Restore support warnings

Actions:

- Restore
- Save as New Session
- Rename
- Delete

Allow users to inspect the session before restoring.

---

# PART 13 — ARCHITECTURE

## 36. Major components

Design these core modules:

1. Application shell
2. Workspace repository
3. Session repository
4. Project registry
5. Application discovery service
6. Project detector
7. Project bootstrapper
8. Startup orchestration engine
9. Process supervisor
10. Readiness checker
11. Browser launcher
12. Autostart manager
13. Shutdown coordinator
14. Linux integration adapters
15. Local persistence
16. Logging and diagnostics

Keep the React interface independent of Linux implementation details.

Use typed Go interfaces where they simplify testing or isolate OS integration.

Do not create unnecessary abstractions or empty layers.

## 37. Suggested repository structure

Adapt to the official Wails v3 project layout.

```text
workspace-manager/
├── cmd/
│   └── workspace-manager/
├── internal/
│   ├── app/
│   ├── domain/
│   ├── workspaces/
│   ├── sessions/
│   ├── projects/
│   │   ├── detection/
│   │   └── bootstrap/
│   ├── applications/
│   ├── launcher/
│   ├── processes/
│   ├── readiness/
│   ├── startup/
│   ├── shutdown/
│   ├── storage/
│   └── platform/
│       └── linux/
├── frontend/
│   └── src/
│       ├── components/
│       ├── pages/
│       ├── hooks/
│       ├── services/
│       ├── types/
│       └── lib/
├── packaging/
├── tests/
├── README.md
├── go.mod
└── package.json
```

Avoid blindly generating this exact tree if Wails requires different bootstrap conventions.

## 38. Local configuration storage

Use XDG locations.

Suggested:

```text
~/.config/workspace-manager/
  settings.json
  profiles/
  projects/

~/.local/state/workspace-manager/
  sessions/
  logs/
  runtime/

~/.cache/workspace-manager/
```

Use environment-based XDG locations rather than hardcoded home paths.

Version the configuration schema.

Support migrations when fields change.

Write JSON atomically.

Use suitable filesystem permissions.

Protect against corruption and concurrent updates.

## 39. Controller and frontend communication

If the controller runs independently:

- Define typed operations.
- Keep communication local.
- Authenticate access using user-session permissions.
- Stream task status to the GUI.
- Prevent concurrent controllers.
- Support reconnecting the GUI.

Do not bind an unrestricted control endpoint to a public network interface.

---

# PART 14 — SECURITY AND SAFETY

## 40. Command execution policy

Implement these rules:

- Never execute detected project commands without user approval.
- Show the exact executable, arguments and working directory.
- Prefer direct process execution over shell execution.
- Use shells only when explicitly configured.
- Do not concatenate untrusted input into shell strings.
- Validate path boundaries.
- Restrict automatic cleanup to tool-owned files.
- Do not persist raw secrets.
- Avoid logging tokens.
- Never automatically push repositories.
- Never delete a user's project directory.
- Avoid unnecessary administrator privileges.

## 41. Filesystem safety

For project creation:

- Reject unsafe target paths.
- Prevent existing file overwrites.
- Avoid symlink escapes.
- Validate generated file destinations.
- Use staging directories where practical.
- Handle partially completed initialization.
- Never delete files whose ownership cannot be established.

## 42. Shutdown safety

Shutdown must be an explicit user action.

No automatic shutdown after project creation.

No forced termination of unrelated processes.

Test shutdown behavior using mocks before manually testing OS integration.

Do not run actual shutdown commands during automated tests.

---

# PART 15 — TESTING

## 43. Go unit tests

Cover:

- Workspace CRUD
- Project registry
- Session snapshot serialization
- Session branching
- Session restore merging
- Startup mode selection
- JSON persistence
- Configuration migration
- Project detection
- Template validation
- Command approval
- Command execution validation
- Readiness checks
- Task state transitions

## 44. Integration tests

Cover:

- Launching test executables
- Working directory handling
- stdout/stderr capture
- Process cancellation
- Graceful process termination
- Duplicate-launch prevention
- Project template initialization
- Failed dependency installation
- Session corruption recovery
- Autostart file lifecycle

Use isolated temporary directories.

## 45. Frontend tests

Cover:

- Create workspace
- Edit workspace
- Add imported project
- Run project detection
- Create new project
- Confirm initialization
- Run & Preview controls
- Restore session
- Branch session
- Cancel shutdown
- Display failures

## 46. Manual Linux tests

Manually verify:

- GNOME Wayland compatibility
- Login startup
- Application discovery
- Browser opening
- VS Code project launching
- Wails window close/reopen
- Project process persistence
- Process supervision
- Save & Shutdown
- Restore after restart

Use explicit manual approval for any test that actually powers off the machine.

---

# PART 16 — IMPLEMENTATION ROADMAP

We will implement the application incrementally.

## Phase 1 — Environment and foundation

- Verify Ubuntu setup.
- Confirm Go, Node and Wails requirements.
- Initialize repository.
- Create Wails + React + TypeScript application.
- Configure styling.
- Confirm desktop window launches.
- Establish initial code organization.

Deliverable: running desktop application.

## Phase 2 — Data models and persistence

- Workspace model
- Project model
- Session model
- Settings model
- JSON repositories
- Validation
- Atomic writes
- Tests

Deliverable: reliable local storage.

## Phase 3 — Workspace manager

- Workspace CRUD
- Workspace editor
- Default workspace
- Profile duplication
- UI integration

Deliverable: usable workspace-management interface.

## Phase 4 — Application discovery and launch

- Desktop entry discovery
- Installed application picker
- Application launching
- Launch status
- Duplicate behavior

Deliverable: launch real desktop applications.

## Phase 5 — Existing project import and detection

- Directory picker
- Project scanners
- Framework detection
- Startup suggestions
- User approvals
- Project configuration storage

Deliverable: import and configure an existing project.

## Phase 6 — Project bootstrapper

- Template registry
- Go starter
- Node.js + TypeScript starter
- Next.js starter
- Python starter
- FastAPI starter
- Git initialization
- Dependency setup
- Project creation UI

Deliverable: create new projects.

## Phase 7 — Startup engine and Run & Preview

- Task engine
- Process supervision
- Startup logs
- Readiness checks
- HTTP preview integration
- CLI output handling
- Retry/cancellation

Deliverable: initialize, run and inspect a project successfully.

## Phase 8 — Complete workspace execution

- Launch multiple apps
- Launch multiple projects
- Open editors
- Open URLs
- Launch delays
- Workspace switching
- Task deduplication

Deliverable: launch a full developer workspace.

## Phase 9 — Session management

- Capture supported state
- Save sessions
- Restore sessions
- Session branching
- Update session
- Snapshot history
- Restore warnings

Deliverable: resume or branch earlier development sessions.

## Phase 10 — Automatic login startup

- XDG Autostart integration
- Startup mode selection
- Fixed mode
- Restore mode
- Hybrid mode
- Controller lifecycle

Deliverable: automatic workspace setup after Ubuntu login.

## Phase 11 — Save & Shutdown

- Confirmation UI
- Snapshot capture
- Managed service cleanup
- Safe shutdown request
- Error handling
- Cancellation behavior

Deliverable: reliable Save & Shutdown workflow.

## Phase 12 — Reliability and UI polish

- Better error messages
- Empty states
- Logs
- Loading indicators
- Keyboard navigation
- Visual consistency
- Accessibility
- Crash recovery improvements

Deliverable: polished daily-use application.

## Phase 13 — Packaging and release

- Production build
- Linux executable
- .deb package
- Desktop launcher
- Application icon
- Installation instructions
- Uninstallation instructions
- README
- Screenshots
- Demo workflow
- License

Deliverable: installable V1 Linux release.

---

# PART 17 — V1 ACCEPTANCE SCENARIOS

V1 must pass these practical user scenarios.

## Scenario A — Daily startup

1. User creates Coding workspace.
2. Adds Chrome, Brave and VS Code.
3. Imports an existing project.
4. Approves startup command.
5. Enables automatic login startup.
6. Logs out and back in.
7. Workspace launches successfully.

## Scenario B — Create and run a new project

1. User selects New Project.
2. Chooses Go.
3. Enters project name.
4. Selects directory.
5. Enables Git initialization.
6. Reviews generated files.
7. Confirms creation.
8. Project is initialized.
9. Project opens in VS Code.
10. User runs generated application.
11. Output appears in the application.
12. Project is added to Coding workspace.

Repeat with Next.js, verifying localhost preview.

## Scenario C — Restore and branch a session

1. User saves a Coding session.
2. Later restores that session.
3. Opens a new project.
4. Starts another development server.
5. Selects Save as New Session.
6. New session is created.
7. Original session remains unchanged.
8. Both sessions remain independently restorable.

## Scenario D — Hybrid startup

1. User saves a session.
2. User configures a default workspace containing additional applications.
3. Selects Hybrid Mode.
4. Logs in again.
5. Previous supported session content is restored.
6. Missing default applications launch.
7. Duplicate servers are not started.

## Scenario E — Save & Shutdown

1. User has applications and managed development servers running.
2. User clicks Save & Shutdown.
3. Application explains what can be restored.
4. User confirms.
5. Snapshot saves successfully.
6. Managed processes stop gracefully.
7. Normal OS shutdown is requested.
8. On next login, the saved session can be restored.

## Scenario F — Error recovery

1. User imports a project with missing dependencies.
2. Tool reports missing executables.
3. User modifies the startup command.
4. Tool saves approved configuration.
5. User retries.
6. Errors are handled without data loss or application crashes.

---

# PART 18 — OUT OF SCOPE

Do not implement these in V1:

- Windows support
- macOS support
- Cloud synchronization
- Login/accounts
- AI-assisted project generation
- AI command execution
- Plugin marketplace
- Remote development
- Kubernetes orchestration
- Complex dependency DAGs
- Arbitrary GUI window positioning
- Exact unsaved editor state restoration
- Browser tab extraction
- Terminal scrollback recovery
- Universal interception of Ubuntu's power menu
- Automatic forced shutdown
- Automatic cleanup of unrelated system processes

Do not add unrelated features unless they are necessary for a working V1.

---

# PART 19 — HOW WE WILL IMPLEMENT THIS TOGETHER

This is extremely important.

I want to learn while building the project, not just receive a large amount of code.

Work with me as a senior engineer and implementation mentor.

Follow these rules:

1. Begin with a critical architectural review.
2. Check the current official documentation for major dependencies.
3. Identify Linux/Wayland restrictions.
4. Recommend the simplest sound architecture.
5. Explain the responsibilities of the major components.
6. Present the implementation roadmap.
7. Start Phase 1 only after the architecture is clear.
8. Implement one file or one small logical unit at a time.
9. Provide complete code for each implemented unit.
10. Explain why important design choices are made.
11. Include exact commands to build, run and test.
12. Avoid pseudocode in actual implementation files.
13. Write meaningful tests as each module is implemented.
14. Avoid creating unnecessary empty packages.
15. Do not silently skip requirements.
16. Do not claim features work without validating them.
17. Do not move to the next phase until the current phase has been tested.
18. Keep the application usable after each major milestone.

Use idiomatic Go, clean React components, strong TypeScript typing, proper error handling and sensible concurrency.

Keep external dependencies limited and justified.

When a design choice has tradeoffs, explain them and recommend one approach.

Do not overcomplicate V1 in anticipation of distant future features.

## First response required

Before writing any application code, provide:

1. Feasibility review for Ubuntu 26.04 GNOME Wayland.
2. Confirmed Wails v3 compatibility and dependencies.
3. Recommended GUI/controller lifecycle.
4. Core domain model.
5. Suggested repository structure.
6. Major interfaces and module boundaries.
7. Process management strategy.
8. Session snapshot and restore limitations.
9. Project scaffolding strategy.
10. Shutdown integration and safety strategy.
11. Revised implementation roadmap if needed.
12. Exact Phase 1 starting point.

Then begin implementation in small, testable steps.

The application must ultimately deliver a complete local developer lifecycle:

**Restore or create → Initialize → Launch → Run → Preview → Save → Shutdown → Resume.**

Make the final product practical enough for everyday use, polished enough for open-source release, and engineered well enough to demonstrate professional Go and Linux development skills.