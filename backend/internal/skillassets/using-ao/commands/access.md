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
| `session` | `AO_SESSION_ID`, set in every session | None, unless a token started the session: then that token's grant |
| `token` | `AO_TOKEN=aoa_...`, outside a session | The token's grant |

Inside a session the caller is always the session. `AO_TOKEN` is not sent
there, so a token exported in a shell profile does not change what sessions may
do, and `ao whoami` says so. Outside a session, an `AO_TOKEN` that does not
start with `aoa_` is refused with exit 4. It is never treated as no token.

A grant is a preset (`read`, `operate`, `manage`) or named groups, optionally
limited to project ids, with an optional expiry.

## What you may run: ao actions

```
ao actions ls [--group <group>] [--json]
ao actions show <action>
```

`ls` prints each catalog action, its group, and whether your grant allows it.
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
| 4 | Refused: the grant lacks the group, the project is outside the grant, the route is not a catalog action, or the token is unknown, revoked or expired |
| 1 | Any other failure |

When a call is refused, do not retry it and do not try another route. Tell the
user which action and group were refused; only they can widen the grant.

## Tokens for agents outside the app: ao token

Run these only when the user asks for a token. They answer only to a caller
with no grant on the machine itself.

```
ao token create <name> [--preset read|operate|manage] [--group <group>]... [--project <id>]... [--expires 30d] [--json]
ao token ls [--json]
ao token revoke <id-or-name>
```

- A name cannot start with `tok_`: that prefix marks a token id, and `revoke`
  takes either.
- `create` prints the secret once. Give it to the other agent as `AO_TOKEN`.
  Never write it into a file in the repository, a commit, a PR or a chat message
  that leaves the machine.
- A session that a token starts carries the token's grant, and revoking the
  token refuses that session's calls too. Commands about the session itself
  (`ao artifact`, `ao render`, `ao preview`, `ao pane`) and reading the
  session keep working. `ao browser` and computer use work while the grant is
  live. Anything else on itself is judged like any other call: `ao send` to
  itself or `ao session delete` needs the grant.
  `ao report` reaches the orchestrator, so it is refused once the grant has ended.
- A caller with a grant cannot create or change automations: the sessions an
  automation starts would carry no grant. It can list and delete them.
- With remote access on (`ao remote-host`), the token also works from another
  machine as `Authorization: Bearer aoa_...`, held to the same grant.

A grant limits calls to the daemon. It does not confine a process that runs as
the same OS user, and it does not limit what a session it starts or messages
does in its shell.
