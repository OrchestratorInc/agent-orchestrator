# ao account

List this machine's Claude Code and Codex subscription accounts, set the
default account for new sessions, and move a session to another account.

The commands act on the daemon of the machine they run on. On a remote host,
they list and change that host's accounts.

Adding, signing in, signing out, and removing accounts stay in the desktop app
(Settings > Harness > Account): signing in needs a person in a browser.

## Syntax

```
ao account ls [--agent claude-code|codex] [--refresh] [--json]
ao account default <account-id>
ao account show [session-id] [--json]
ao account switch <session-id> <account-id> [--stop-turn] [--wait] [--timeout 3m]
```

`show` without a session id uses this session (`AO_SESSION_ID`).

## Flags

| Command | Flag | Meaning | Default |
|---|---|---|---|
| `ls` | `--agent` | Only `claude-code` or `codex` accounts | All |
| `ls` | `--refresh` | Read sign-in state and limits again instead of cached values | Off |
| `switch` | `--stop-turn` | Stop a turn in progress now instead of waiting for it to finish | Off |
| `switch` | `--wait` | Return only when the move completes or fails | Off |
| `switch` | `--timeout` | How long `--wait` waits | `3m` |

## Moving a session

A move restarts the agent in the same conversation on the new account. The
session keeps its history. A signed-out account is refused with
`AGENT_ACCOUNT_SIGNED_OUT`, and the session stays where it was.

A working agent finishes its turn first. `--stop-turn` stops the turn now; use
it only when the user asks, or when the agent is stuck on the old account's
limit.

To move your own session, run `switch` without `--wait`: the move starts after
your turn ends, so waiting for it in that turn never finishes.

## Starting on an account

`ao spawn --account <account-id>` starts a worker on that account instead of
the default.

One email can have accounts in several organizations. Tell them apart by the
`ORG` column, or by `orgId` and `orgName` in `ls --json`.

```bash
ao account ls --agent claude-code
ao spawn --agent claude-code --account <account-id> --name "account-worker" --prompt "..."
ao account switch mer-3 <account-id> --wait
```
