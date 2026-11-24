# ao whoami, ao actions, ao call, ao token

How the daemon identifies a caller, what a caller may do, and how to run an
action that has no command of its own.

## Who you are: ao whoami

```
ao whoami [--json]
```

| Caller | How the daemon knows it | Limit |
|---|---|---|
| `user` | No token, on this machine | None |
| `session` | `AO_SESSION_ID`, set in every session | None |
| `token` | `AO_TOKEN=aoa_...`, outside a session | The token's grant |

Inside a session the caller is always the session. `AO_TOKEN` is not sent
there, so a token exported in a shell profile does not change what sessions may
do, and `ao whoami` says so. Outside a session, an `AO_TOKEN` that does not
start with `aoa_` is refused with exit 4. It is never treated as no token.

A grant is a level, optionally limited to project ids, with an optional expiry.

| Level | Allows |
|---|---|
| `read` | Look at sessions, projects, automations, accounts and settings |
| `operate` | `read`, and run work: start, message, steer and stop sessions, answer their questions and approvals, merge PRs, run reviews |
| `manage` | `operate`, and change setup or delete: projects, automations, settings, accounts, deleting sessions |

## What you may run: ao actions

```
ao actions ls [--json]
ao actions show <action>
```

`ls` prints each catalog action, its level, and whether your grant allows it.
`show` prints one action's input: path and query parameters and the JSON body
schema. A caller with a grant can reach only catalog actions.

## Run one action: ao call

```
ao call <action> [--input '<json object>' | --input -]
```

`--input` is one JSON object. Keys that name a path or query parameter go
there; the rest form the JSON body. The result is printed as JSON.

```bash
ao call session.list --input '{"project":"api"}'
ao call session.spawn --input '{"projectId":"api","harness":"claude-code","displayName":"Fix login","prompt":"..."}'
ao call chat.input.resolve --input '{"sessionId":"api-3","requestId":"<id>","action":"accept","content":{"question_0":"sqlite"}}'
```

Prefer a typed command (`ao spawn`, `ao send`, `ao session ...`) when one
exists. Use `ao call` for the rest, such as answering another session's
question or approval: read the pending request's `requestId` and schema from
`ao call session.conversation --input '{"sessionId":"<id>"}'`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Done |
| 2 | Wrong usage, including a missing input field |
| 3 | The catalog has no such action |
| 4 | Refused: the grant's level is too low, the project is outside the grant, the route is not a catalog action, or the token is unknown, revoked or expired |
| 1 | Any other failure |

When a call is refused, do not retry it and do not try another route. Tell the
user which action and level were refused; only they can widen the grant.

## Tokens for agents outside the app: ao token

Run these only when the user asks for a token. They answer only to a caller
with no grant on the machine itself.

```
ao token create <name> [--level read|operate|manage] [--project <id>]... [--expires 30d] [--json]
ao token ls [--json]
ao token revoke <id-or-name>
```

- `--level` defaults to `read`.
- Only `read` is a hard limit. A token that can start or message a session
  (`operate` and `manage`) can have that session run any command as the user,
  so those levels are for an agent the user trusts with the machine. They
  differ in what the agent can do by mistake.
- A session that a token starts is a session like any other: it has no limit,
  and it keeps running when the token is revoked.
- A name cannot start with `tok_`: that prefix marks a token id, and `revoke`
  takes either.
- An empty `--project` value is refused: an empty limit would mean every project.
- With a token limited to projects, name the project in each command
  (`ao spawn --project api`). The CLI cannot pick it from the working
  directory, because listing every project is refused.
- A token limited to projects cannot run an action that reaches every project:
  machine-wide settings, the default account, automations. It can read settings.
- A token that has expired no longer holds its name or counts toward the
  limit of 64 tokens.
- `create` prints the secret once. Give it to the other agent as `AO_TOKEN`.
  Never write it into a file in the repository, a commit, a PR or a chat message
  that leaves the machine.
- With remote access on (`ao remote-host`), the token also works from another
  machine as `Authorization: Bearer aoa_...`, held to the same grant.

A grant limits calls to the daemon. It does not confine a process that runs as
the same OS user.
