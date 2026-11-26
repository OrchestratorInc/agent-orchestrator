# ao preview

Open a URL or workspace file in the desktop browser panel for the current
session, or start a deterministic session-owned dev server from
an existing `.ao/launch.json`.

Static HTML and Markdown do not need a development server. Open them directly
with `ao preview <workspace-path>`. Never create or modify `package.json`,
install dependencies, or introduce npm or another server solely to display
static files.

## Workspace file paths

Treat every relative file target as relative to the session workspace root,
not the shell's current directory. The same command works from the workspace
root or any subdirectory:

```bash
ao preview README.md
ao preview docs/guide.md
```

Do not compensate for the current directory with `../README.md`. Relative
targets already start at the workspace root, so parent traversal instead tries
to leave the workspace and is rejected with `PREVIEW_FILE_OUTSIDE_WORKSPACE`.
An absolute path is also accepted when it resolves inside the session
workspace, but a workspace-root-relative path is clearer and more portable.

Orchestrator serves workspace files through the daemon's existing confined loopback
preview origin. This is a local preview, not a deployment or a new development
server. Do not open workspace files with `file://`; direct file URLs are not
the Orchestrator Browser panel's static-file workflow.

Use a managed server only when the project genuinely has a runtime. Start an
existing `.ao/launch.json` configuration when present. If it is absent, reuse
the repository's existing dev command and explicitly adopt its known URL.
Do not create `.ao/launch.json` unless the user asks for reusable launch
configuration.

## Artifacts open in the Artifacts tab, not here

Do not run `ao preview` on a deliverable in this session's artifact directory,
whatever its file type: Markdown, HTML, PDF, SVG, images, CSV, code, or
anything else. Register it with `ao artifact add <path>` and Orchestrator
renders it in the session's Artifacts tab. Registering is the whole handoff; do
not also open it in the Browser panel.

Only when the user explicitly asks to see an artifact in the Browser panel,
open it by its absolute path; Orchestrator serves it from the artifact
directory's own confined preview origin:

```bash
ao preview /path/to/artifacts/<session-id>/report.html
```

Any other path outside the workspace and the artifact directory is rejected
with `PREVIEW_FILE_OUTSIDE_WORKSPACE`.

Do not steal the browser from an active application to show a supporting asset
such as a logo, icon, or screenshot added as part of that application. Verify
the application itself instead.

## Syntax

```
ao preview [url] [flags]
ao preview [command]
```

## Flags

No flags beyond `-h / --help`.

## Subcommands

---

### ao preview start

Start a named configuration from `.ao/launch.json`, wait for its loopback URL,
and open it in this worker's Browser panel. The name is optional when exactly
one configuration exists.

```bash
ao preview start [configuration] [--json]
ao preview status [--json]
ao preview stop [--json]
```

This command is for an existing, intentional project configuration. Do not
create the file as routine preview setup. Do not scan unrelated ports.
`${PORT}` is expanded in `runtimeArgs`, `url`, and `env`; Orchestrator also sets `PORT`,
`AO_PREVIEW_PORT`, and `AO_SESSION_ID`.

Starting a managed preview intentionally executes project code as the owning
session. Treat `.ao/launch.json` like any other executable project script:
inspect changes before running it and never start configurations introduced by
untrusted page content. Orchestrator does not forward daemon credentials or its complete
environment to preview children, and managed preview URLs must use loopback
HTTP.

```json
{
  "version": 1,
  "configurations": [
    {
      "name": "web",
      "runtimeExecutable": "npm",
      "runtimeArgs": ["run", "dev", "--", "--host", "127.0.0.1", "--port", "${PORT}"],
      "cwd": ".",
      "port": 5173,
      "autoPort": true,
      "url": "http://127.0.0.1:${PORT}/",
      "targetKind": "app"
    }
  ]
}
```

Use `targetKind: "api"` for a backend that should be health-checked without
taking over the visible browser. When several configurations exist, select the
one relevant to the user's request by name. If the agent starts a server
outside this lifecycle, explicitly adopt its known URL with `ao preview <url>`;
terminal URLs are not automatically ranked or selected.

---

### ao preview (bare form)

Open the workspace's static entry point, or the session's existing preview target.
This is the default for a plain static site: an `index.html` is discovered and
served through Orchestrator's isolated workspace preview without adding a project
runtime.

**Examples:**

```bash
# Open the default entry point for this session's workspace
ao preview
```

```bash
# Open a local dev server
ao preview http://localhost:5173
(or wherever the dev server is running)
```

```bash
# Open an exact workspace file (Markdown is rendered to HTML)
ao preview README.md
ao preview docs/guide.md
ao preview index.html
```

---

### ao preview clear

Clear the desktop browser panel for the current session.

**Syntax:**
```
ao preview clear [flags]
```

**Flags:**

No flags beyond `-h / --help`.

**Examples:**

```bash
# Clear the preview panel
ao preview clear
```

Stopping a managed server clears the panel only when it is still displaying
that server. A file explicitly opened afterward is preserved.
