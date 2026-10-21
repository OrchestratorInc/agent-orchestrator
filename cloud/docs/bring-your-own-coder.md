# Bring your own Coder: template setup guide

This guide is for teams that connect their own Coder deployment to AO Cloud and
run AO sessions on their own templates. It covers how AO uses a template, what
the template needs, and how to set up a template that prepares its own
repositories and services. The provider internals are in
[coder-sandbox-provider.md](coder-sandbox-provider.md).

The connection settings below are in the desktop app under **Settings >
Coder**. Connect the Coder URL and an API token first; the **Workspace setup**
section appears once the connection is saved. Only organization owners and
admins can change it, and changes apply to sessions started after you save.
Per-session Setup and Cleanup commands are project settings, the same ones a
local project has: open the project's settings and choose **Scripts**.

## How AO uses your template

- **One Coder workspace per AO session.** The orchestrator and every worker
  session each get their own workspace, created from the template chosen for
  the project. Concurrent sessions never share a workspace, a checkout or a
  shell.
- **Workspace names.** An orchestrator's workspace is named
  `<prefix>-o-<id>` and a worker's `<prefix>-w-<word>-<id>`, where `<id>` is
  the first 12 characters of the session id and `<word>` is the first word of
  the chat's name when the session was created (lowercase letters and digits,
  at most 10 characters, left out when the name has none). For a
  bring-your-own Coder the prefix defaults to the project name, lowercased
  and shortened to fit Coder's naming rules; set **Workspace name prefix** in
  the project's settings (or when creating the project) to choose another.
  When everything does not fit Coder's 32 characters, the word (down to 3
  characters) and then the prefix are shortened; the `-o-`/`-w-` marker and
  the 12-character id are always kept. If Coder reports the name as already
  taken by another workspace, AO tries once more with a longer id that fills
  the 32 characters, and keeps that name. AO records the name when it creates
  the session and never changes it otherwise: renaming the chat does not
  rename the workspace. Sessions created before
  this scheme keep their original `<prefix>-<id>` (or `ao-<id>`) workspace
  names, and so do new sessions while the control plane's
  `AO_CLOUD_CODER_READABLE_NAMES` is off (see
  [Coder rollout flags](deployment.md#coder-rollout-flags)). The session's top bar shows the workspace name with a link that
  opens it in Coder, for as long as the session exists.
- **Durable root.** AO keeps its own state under the workspace user's home
  (`$HOME/.ao`): the worker, its logs, and the agent's configuration and
  conversations. Without a working directory or a project working folder
  (below), the session's checkout is `$HOME/repository`.
- **Lifecycle.** AO starts, stops and deletes the workspaces it created. A
  startup failure marks the session failed with a message in the app; AO never
  stops or deletes your workspace because of it.

## Template requirements

- Linux, with a `coder_agent` whose `arch` is `amd64` or `arm64`.
- `bash` and `git` on the `PATH`.
- If AO runs as its own `ao-worker` user (the default), the workspace user needs
  passwordless `sudo` so AO can create that user. With **Run the agent as the
  workspace user** on, AO needs no `sudo` at all.
- The coding agent's CLI (for example Claude Code) may be baked into the image;
  AO installs or upgrades what it needs otherwise. If your template installs it
  in the background after the workspace reports ready, AO keeps checking for it
  (every 10 seconds, for up to 20 minutes) and starts the agent once it
  appears.

## Agent browser

The agent tabs in the session's Browser panel show a headless Chromium that AO
starts inside the workspace the first time the agent uses it. For that, the
template's image must contain:

- Chromium, as `chromium` on the `PATH` or at `/usr/bin/chromium`. On Ubuntu,
  `chromium-browser` only installs a snap, which AO does not support; use
  another build, such as Playwright's, linked to `/usr/local/bin/chromium`.
- agent-browser 0.38.1 at `/usr/local/bin/agent-browser`: the
  `agent-browser-linux-x64` or `agent-browser-linux-arm64` file of its
  [release](https://github.com/vercel-labs/agent-browser/releases/tag/v0.38.1).

`cloud/coder/Sandbox.Dockerfile` installs both, with fonts for non-Latin
pages. Without them, sessions work as usual but have no agent browser.

## When AO starts working

AO waits until the Coder agent is connected and its lifecycle is `ready`, which
means every startup script of the template (`coder_script` resources and the
agent's `startup_script`) has finished. Only then does it install its worker,
check out the repository and start the agent.

If your template reports completion some other way, for example a custom
"Launch Status" that keeps running after the startup scripts exit, set
**Workspace ready check** to a command that succeeds only once setup is done.
AO runs it as the workspace user every 5 seconds until it exits 0, for up to the
ready check timeout (900 seconds unless you set one, 30 to 3600):

```sh
grep -q '^Ready' ~/.launch-status
```

```sh
test -f ~/work/.bootstrap-complete && docker compose -f ~/work/alice/compose.yaml ps --status running --quiet | grep -q .
```

## Recommended setup for templates that prepare `~/work/<repo>`

Many templates clone the team's repositories into `~/work/<repo>`, write
`.env` files and start services before handing the workspace to a person. To
have AO work in that same checkout, with the same access:

1. **Run the agent as the workspace user: on.** The worker and the agent run as
   your template's login user, with its home, groups and Docker access, so they
   can use the files the template prepared (including a `.env` readable only by
   that user).
2. **Working directory: `~/work/{repo}`.** `{repo}` is replaced by the
   repository name. AO adopts the checkout already there: it switches it to the
   session branch and saves any local changes to tracked files in a stash named
   after the session. If the directory does not exist, AO clones into it. The
   path may be absolute or start with `~/` or `$HOME/`, must name a directory
   below that root, and may not contain `.` or `..` segments.
3. **Workspace ready check** if your template signals readiness itself (above).
4. **Project Setup and Cleanup scripts** for anything per session (below).
5. **Keep the agent out of secret files (.env): on** (the default). This
   matters most once the agent runs as the workspace user, because it then has
   the access to read them. See [Secret files](#secret-files).

Without a working directory, **Use the template's existing checkout** (on by
default with the workspace user) makes AO look for a checkout of the session's
repository directly under `~/work` and then under `~`, matching on the `origin`
remote, and falls back to a fresh clone in `$HOME/repository`.

### Working folder with the `ao-worker` user

With **Run the agent as the workspace user** off (the default), and the
control plane's `AO_CLOUD_CODER_WORKER_WORKING_FOLDER` on (see
[Coder rollout flags](deployment.md#coder-rollout-flags)), a project's
**Working folder** (Project settings > Repositories, for example
`~/work/{repo}`) is where AO clones the main repository instead of
`$HOME/repository`, and the additional repositories go beside it
(`~/work/<name>`). `~` is the workspace login user's home; `{repo}` is the
repository name, fixed when the session is created. The folder must be inside
the connection's durable root (your home by default, so start it with `~/`):
a folder outside it, such as `/workspace/{repo}` on a Docker template, loses
the agent's uncommitted work when the workspace restarts. AO refuses such a
folder when the project is saved, or, when that is only known inside the
workspace, stops the session with "The working folder must be inside ... so
the agent's work survives restarts." The bootstrap creates missing parent
folders below your home (owned by the login user; the checkout's own parent
is group-writable for `ao-worker`) and the checkout folders (owned by
`ao-worker`), and adds traverse permission (`o+x`) to your home or a folder
below it that `ao-worker` cannot enter, never to `/`, `/root` or a folder
outside your home. If it cannot prepare those folders the session stops with
"The working directory can't be used" and the reason. It
never takes over or changes a folder that already exists: if the template
already cloned `~/work/<repo>` (for example because the project ticked that in
its **Template options**), the session stops with "The working folder is already
in use". Untick it in the Template options, or turn on **Run the agent as the
workspace user** to work in the template's checkout instead. Such a session
never opens its terminal in that folder, and deleting it runs no Cleanup
("Cleanup did not run: this session never got its working folder"), so the
project's Cleanup never touches the template's folder. An additional
repository's folder beside it that the template already cloned (say
`~/work/billing`) is refused the same way: the session starts on its other
repositories, logs that it skipped that one, and shows that repository's Setup
and Cleanup steps as skipped ("... already exists (created by your
Coder template), so AO did not run this step in it."), never running them in
the template's folder. A restarted or
restored session keeps using AO's own folder. The additional repositories'
folders are prepared for the repositories the project lists when the session
is created; a repository added later is checked out in new sessions (an
existing session skips it, and logs why, when it cannot create its folder).

### What goes where

Keep machine-level setup in the template and per-session setup in the
project's Setup script:

| Template (runs once per workspace build) | Project Setup script (runs for the session) |
| --- | --- |
| Install tools, language runtimes, Docker | Bring up the session's own services |
| Credentials, registries, shared caches | Migrations or seed data for the session |
| Clone repositories, write `.env` | Anything that depends on the session branch |

## Setup and Cleanup scripts

A project's **Scripts** settings work for cloud projects as they do for local
ones: open the project's settings, choose **Scripts**, and add commands under
**Setup** and **Cleanup**. They belong to the project, so every session of the
project, orchestrator and workers alike, runs the same commands, and a
connection setting is not needed for them. Only organization owners and admins
can change them, since they run in every member's sessions; members see them
read-only.

- **Setup** commands run in order with `sh -c`, in the session's working
  directory, after the repository checkout and before the agent starts. They
  run once per session, when its workspace is first prepared; a worker restart
  or a workspace stop and start keeps the prepared checkout and does not run
  them again, and a session that started before the project had Setup never
  runs Setup added later. Each command may run for its time limit: 30
  minutes unless you set one for the step (1 to 120 minutes) in **Project
  settings > Scripts**. If one fails or times out, the agent does not start: the session
  shows "The project's Setup script failed at step N" with the end of the
  command's output, and
  reopening the session runs Setup again with the project's current commands,
  so fixing the Setup script and retrying is enough. This matches a local
  project, where a failed setup step stops the launch.
- **Cleanup** commands run in order before AO deletes the session's workspace,
  in the same directory and environment, as the same user as the agent. They do
  not run when a workspace is stopped or paused. Cleanup is best effort: each command
  has its time limit (10 minutes unless you set one for the step), the whole
  run has the sum of them (at least 10 minutes, at most 2 hours), a failed
  command stops the remaining ones, and the workspace is deleted either way. Like a local project, AO uses the project's
  Cleanup as it is when the workspace is deleted, so editing it after a session
  started still applies to that session, and removing the project runs its
  last Cleanup in each of its workspaces. (A Cleanup longer than 12 KB in total
  runs as it was when the session started.) Cleanup currently runs only on
  Coder; other cloud sandboxes are deleted without it.
- Up to 20 commands per list, each at most 4 KB.

Environment available to both:

| Variable | Value |
| --- | --- |
| `AO_SESSION_ID` | The AO session id |
| `AO_SESSION_KIND` | `orchestrator` or `worker` |
| `AO_SESSION_BRANCH` | The session's git branch |
| `AO_WORKSPACE_DIR` | The working directory the session uses |
| `AO_WORKTREE_PATH` | The same directory, under the name local projects use |
| `AO_SOURCE_TREE_PATH` | The same directory: a cloud session has no separate project folder |
| `AO_REPOSITORY_URL` | The project's repository URL |
| `AO_PROJECT_ID` | The AO project id |
| `AO_PROJECT_NAME` | The AO project name |
| `CODER_WORKSPACE_NAME` | The Coder workspace name (with the workspace user) |

### Namespacing per-session stacks

Each session has its own workspace today, but name everything Setup starts
after the session anyway. Names then never collide when two workspaces share a
Docker host, and Cleanup can find exactly what Setup started. Setup commands
run one by one, so keep each step self-contained:

```sh
# Setup, step 1
docker compose -p "ao-$AO_SESSION_ID" up -d --wait
```

```sh
# Setup, step 2
docker compose -p "ao-$AO_SESSION_ID" exec -T app make db-migrate
```

```sh
# Cleanup, step 1
docker compose -p "ao-$AO_SESSION_ID" down --remove-orphans
```

## Secret files

With **Keep the agent out of secret files** on, a session's agent cannot read
or edit `.env` or `.env.<name>` files (for example `.env.local`). Committed
templates named `.env.example`, `.env.sample` and `.env.template` stay readable.

- **Claude Code** gets permission rules that stop its file tools, and file
  commands such as `cat`, `grep` or `cp`, from touching those files. The rules
  hold in every permission mode, including auto-approve.
- **Claude Code and Codex** also run an AO check before each shell command,
  in the terminal and in Chat. A command that names one of those files is
  blocked, for example `python3 -c "open('.env')"` or `source .env`, and the
  agent is told to use the project's own `make` targets or scripts instead.
  That includes harmless commands such as `echo .env >> .gitignore`; the agent
  can make that change with its file-editing tools instead. Codex edits that
  create, change or delete one of those files are blocked too.
- **Commands that load `.env` without naming it still run**, such as
  `make db-host` or `docker compose up`. That is how a project uses its
  secrets without the agent seeing them.

Codex Chat needs a Codex with session hooks (0.147 or newer); an older Codex
runs Chat without the shell check, and AO logs a warning in the worker log.

This keeps the agent from reading secrets by accident or out of habit. It is
not a hard boundary: a program the agent writes could still open the file, and
Cursor and OpenCode sessions are not covered. Secrets the agent must never be
able to reach belong outside the workspace user's access, for example by
running the agent as `ao-worker` and keeping the file readable only by the
workspace user.

## Troubleshooting

- **The agent works in `~/repository` instead of your checkout.** Turn on
  **Run the agent as the workspace user** and set the working directory; both
  apply to new sessions only.
- **"The working folder is already in use".** The project's working folder
  already exists in the workspace and is not AO's (usually the template cloned
  it). Untick that in the project's Template options, or turn on **Run the
  agent as the workspace user** to reuse it, then start a new session.
- **"Permission denied" on files the template created.** The agent is running
  as `ao-worker`. Turn on **Run the agent as the workspace user**.
- **The session waits a long time, then fails to start.** The template's
  startup scripts or the ready check did not finish in time. Open the workspace
  from the session's top bar and check its build and agent logs in Coder.
- **"The project's Setup script failed at step N".** One of the project's Setup
  commands exited with an error; the session shows the end of its output. Fix
  the command in the project's **Scripts** settings, then click **Retry**
  (**Retry start** in Chat) or reopen the session.
- **"The working directory can't be used".** Fix the directory in the
  workspace (or the connection's **Working directory**), then reopen the
  session. AO restarts the session's worker to check the directory again, which
  can take a few minutes.
