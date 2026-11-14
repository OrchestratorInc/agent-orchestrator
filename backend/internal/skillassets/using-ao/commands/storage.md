# ao storage

Show how much disk space Orchestrator uses under its data folder and free what it no longer needs. Use this when the user asks why Orchestrator takes so much space or wants to reclaim it.

Cleanup never touches anything outside Orchestrator's data folder. Open sessions keep their source, uncommitted work and history: a quiet session may only have reinstallable dependencies and build output (such as `node_modules`) offloaded, and those come back automatically when it resumes. Worktrees with uncommitted changes are never removed. Git branches and pull requests are always kept.

## Syntax

```
ao storage [--refresh] [--json]
ao storage clean [--dry-run] [--only <kinds>] [--session <id>]... [--include-dev] [--yes] [--json]
ao storage log [--limit <n>] [--json]
ao storage settings [flags]
```

## Cleanup kinds

| Kind | What it frees |
|---|---|
| `offload` | Git-ignored dependency and build folders of quiet open sessions; restored on resume |
| `ended_worktrees` | Worktrees of terminated sessions (never forced; dirty ones are kept; one holding git-ignored files that cannot be rebuilt, such as a `.env`, is never removed automatically and its plan reason lists them) |
| `orphan_worktrees` | Clean git worktrees no session owns (manual only) |
| `db_backups` | Old database backups (the newest is kept) |
| `logs` | Log files not written for a while |
| `dev_profiles` | Unused dev-build data folders (only with `--include-dev` or `--only dev_profiles`) |
| `dependency_templates` | Unused dependency templates kept for fast copy-on-write restores |
| `database` | Free pages inside the database file |
| `trash` | Deletes an earlier cleanup started |

## Flags

### `ao storage`

| Flag | Meaning | Default / Required |
|---|---|---|
| `--refresh` | Measure again before reporting | - |
| `--json` | Output the report as JSON | - |

### `ao storage clean`

| Flag | Meaning | Default / Required |
|---|---|---|
| `--dry-run` | Show what would be cleaned without changing anything | - |
| `--only` | Limit to these kinds (comma-separated) | all kinds except `dev_profiles` |
| `--session` | Limit session cleanups to this session; repeatable | - |
| `--include-dev` | Also remove unused dev-build profiles | - |
| `--yes`, `-y` | Skip the confirmation prompt | - |
| `--json` | Output the plan or result as JSON | - |

### `ao storage settings`

| Flag | Meaning |
|---|---|
| `--auto-clean on\|off` | Let the daemon clean up on its own |
| `--offload on\|off` | Offload dependencies of quiet sessions |
| `--offload-idle-hours <n>` | Quiet hours before offloading a session with an open pull request (default 48) |
| `--offload-no-pr-hours <n>` | Quiet hours before offloading a session with no pull request (default 2) |
| `--offload-merged-minutes <n>` | Quiet minutes after its pull requests merged or closed (default 30) |
| `--ended-worktree-days <n>` | Days before an ended session's worktree is removed; 0 never (default 7) |
| `--backup-days`, `--log-days`, `--template-days`, `--dev-profile-days <n>` | Ages for the other kinds |
| `--clean-dev-profiles on\|off` | Remove unused dev-build profiles automatically |

`AO_STORAGE_AUTOCLEAN=off` in the daemon's environment keeps scheduled passes to logging what they would do.

## Examples

```bash
# See where the space goes
ao storage
```

```bash
# Preview, then run, a cleanup of ended-session worktrees only
ao storage clean --only ended_worktrees --dry-run
ao storage clean --only ended_worktrees
```

```bash
# Offload one quiet session's dependencies now
ao storage clean --only offload --session my-project-12 --yes
```

```bash
# Keep dependencies of quiet sessions for a week
ao storage settings --offload-idle-hours 168
```
