# Set up a project from Conductor

Use this when the user asks to bring a repository's Conductor settings into Orchestrator: its setup and archive scripts, run actions, environment variables, general prompt, or the files Conductor copies into each workspace. Orchestrator imports Conductor chats by itself, but not these settings. You convert them with `ao project set-config` and `ao cue create`.

Conductor's files and database belong to the user. Only read them. Never write, move, or delete anything under `~/Library/Application Support/com.conductor.app` or in the repository's `.conductor/` folder.

## 1. Find the project

Run `ao project ls --json` and match the repository's root folder: the folder Conductor calls the repository, not a workspace under `~/conductor/workspaces/`. If it is not a project yet, add it with `ao project add --path <root>`.

Read what the project already has with `ao project get <id> --json` and `ao cue list --project <id> --json`. Keep all of it.

## 2. Read Conductor's settings

Each later file overrides the earlier ones, key by key:

| File | Notes |
|---|---|
| `~/.conductor/settings.toml` | The user's defaults. Ignore `models` and `tool_approvals_enabled`. |
| `conductor.json` in the repository | Legacy format. Ignore it when `.conductor/settings.toml` exists. |
| `.conductor/settings.toml` | The repository's settings. |
| `.conductor/settings.local.toml` | The user's uncommitted overrides. |

Conductor also keeps settings typed into its app in the repository's row of its database. Read that row read-only. When Conductor is closed and the database has no `-wal` file, open it as `immutable=1` so SQLite does not create `-wal` or `-shm` files next to it:

```sh
db="$HOME/Library/Application Support/com.conductor.app/conductor.db"
uri="file:$HOME/Library/Application%20Support/com.conductor.app/conductor.db?mode=ro"
[ -e "$db-wal" ] || uri="$uri&immutable=1"
sqlite3 -line "$uri" "SELECT * FROM repos WHERE root_path = '<root>'"
```

In `<root>`, write each `'` as `''`. The useful columns are `setup_script`, `archive_script`, `run_script`, `custom_prompt_general`, and `file_include_globs`. If the files and the database disagree, show the user both and ask which to keep.

## 3. Map each setting

| Conductor | Orchestrator |
|---|---|
| `scripts.setup`, `setup_script` | a Setup step: `--post-create` |
| `scripts.archive`, `archive_script` | a Cleanup step: `--pre-remove` |
| `scripts.run` as one string, `run_script` | a command Cue named `Run` |
| `[scripts.run.<name>]` actions | a command Cue named `<name>` for each action; see below |
| `environment_variables.<KEY>` and `environment_variables.local.<KEY>` | project environment: `--env KEY=VALUE` |
| `prompts.general`, `custom_prompt_general` | agent rules: `--agent-rules` |
| `.worktreeinclude`, else `file_include_globs`, else `.env*` | a Setup step that copies those ignored files; see below |
| `environment_variables.cloud`, `git.*`, `run_mode`, `run_script_mode`, other `prompts.*` and `custom_prompt_*` | nothing; list them as skipped |

A run action is a table with `command`, optional `args`, `options.cwd`, `hide`, and `available_in`. Append each of `args` to the command, shell-quoted. When `options.cwd` is set and is not `.`, put `cd -- <cwd> &&` before the command, with `<cwd>` shell-quoted. Skip an action with `hide = true`, and one whose `available_in` does not include `"local"`.

To copy ignored files, put this step before the setup script, so the script finds `.env` when it runs. Pass one `:(glob)` pathspec for each pattern. Prefix `**/` to a pattern that has no `/`, and use `:(exclude,glob)` for a pattern that starts with `!`.

```sh
git -C "$AO_SOURCE_TREE_PATH" ls-files -z --others --ignored --exclude-standard -- ':(glob)**/.env*' | xargs -0 sh -c 'for f; do [ -e "$f" ] || { mkdir -p "$(dirname "$f")" && cp -pR "$AO_SOURCE_TREE_PATH/$f" "$f"; }; done' sh
```

## 4. Replace Conductor variables

Orchestrator does not set `CONDUCTOR_*` variables. Rewrite every command that uses one. Setup and Cleanup steps run with `sh` in the new worktree. A Cue runs in the session's worktree.

| Conductor | In Setup and Cleanup | In a Cue |
|---|---|---|
| `CONDUCTOR_ROOT_PATH` | `$AO_SOURCE_TREE_PATH` | the project folder, written out |
| `CONDUCTOR_WORKSPACE_PATH` | `$AO_WORKTREE_PATH` | `$PWD` |
| `CONDUCTOR_WORKSPACE_NAME` | `$(basename "$AO_WORKTREE_PATH")` | `$(basename "$PWD")` |
| `CONDUCTOR_DEFAULT_BRANCH` | the project's default branch, written out | the same |
| `CONDUCTOR_IS_LOCAL` | `1` | `1` |
| `CONDUCTOR_PORT` | the port expression below | the same with `$PWD` |

Orchestrator gives a worktree no port of its own. This expression derives a stable port from the worktree's path, the first of a block of ten as in Conductor. If two worktrees get the same port, the user can set the port in that command.

```sh
$((10000 + $(printf %s "$AO_WORKTREE_PATH" | cksum | cut -d' ' -f1) % 2270 * 10))
```

## 5. Confirm, then apply

Show the user the Setup and Cleanup steps, Cues, variables, and agent rules you plan to add, and the keys you skip. Apply them only after the user agrees.

`--post-create`, `--pre-remove`, and `--env` replace the whole list or map, and `--agent-rules` replaces the whole text. Pass the existing steps, variables, and rules together with the new ones, in one `ao project set-config` call. Do not add a step the project already has.

```bash
ao project set-config <id> \
  --post-create '<existing step>' --post-create '<copy step>' --post-create '<setup script>' \
  --pre-remove '<existing step>' --pre-remove '<archive script>' \
  --env 'EXISTING_KEY=<existing value>' --env 'API_URL=http://localhost:3000' \
  --agent-rules '<existing rules>

<general prompt>'
ao cue create --project <id> --name dev --command 'PORT=<port expression> npm run dev'
```

`ao cue create` cannot replace a Cue. If a Cue with that name exists, keep it and tell the user. When you finish, tell the user to check **Project settings → Scripts**, **Environment**, and **Cues**.
