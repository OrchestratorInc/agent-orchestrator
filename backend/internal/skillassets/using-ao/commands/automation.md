# ao automation

Manage durable recurring or called session automations through the daemon API.

## Syntax

```text
ao automation <subcommand> [args] [flags]
ao automations <subcommand> [args] [flags]
```

## Subcommands

---

### ao automation create

Create a recurring automation, or one that runs on each POST to its hook address.

**Syntax:**
```text
ao automation create --project <id> --name <name> --prompt <text> (--rrule <rule> | --cron <expr> | --when-called) [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project string` | Project id that owns the automation | Required |
| `--name string` | Display name | Required |
| `--prompt string` | Session task prompt | Required |
| `--rrule string` | RFC 5545 recurrence rule | One of `--rrule`, `--cron`, `--when-called` |
| `--cron string` | Supported five-field cron expression | One of `--rrule`, `--cron`, `--when-called` |
| `--when-called` | Run on each POST to `/hooks/<key>` at this machine's Remote access address | One of `--rrule`, `--cron`, `--when-called` |
| `--timezone string` | IANA timezone; not with `--when-called` | Local zone when resolvable |
| `--hook-secret string` | HMAC-SHA256 secret that signs each call's body | - |
| `--hook-signature-header string` | Header carrying the signature; required with `--hook-secret` | - |
| `--filter string` | `path=a,b`, `path!=a,b`, or `path` under `body`, `headers`, or `query` (repeatable) | - |
| `--dedupe-key string` | Template naming one delivery, such as `{{headers.X-GitHub-Delivery}}` | - |
| `--kind string` | Session kind | `worker` |
| `--harness string` | Agent harness; empty uses project default | - |
| `--disabled` | Create disabled | - |
| `--json` | Print JSON | - |

**Example:**

```bash
ao automation create --project my-app --name "Morning triage" --prompt "Review overnight issues" --cron "0 9 * * *" --timezone America/Los_Angeles
ao automation create --project my-app --name "Issue triage" --prompt "Triage {{body.issue.title}}" --when-called --hook-secret "$SECRET" --hook-signature-header X-Hub-Signature-256 --filter body.action=opened
```

`{{body.*}}`, `{{headers.*}}`, and `{{query.*}}` in a called automation's prompt fill in values from the call, wrapped in tags the agent treats as data. The hook answers 202 when it queues a run and 200 when a filter or the dedupe key drops the call. Each queued call launches once the previous one has launched; disabling the automation drops calls that have not started.

---

### ao automation list

List automations.

**Syntax:**
```text
ao automation list [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project string` | Filter by project id | - |
| `--enabled bool` | Filter by enabled state when present | - |
| `--json` | Print JSON | - |

---

### ao automation get

Show one automation.

**Syntax:**
```text
ao automation get <id> [--json]
```

---

### ao automation update

Update automation metadata, schedule, hook settings, harness, or enabled state.

**Syntax:**
```text
ao automation update <id> [flags]
```

**Flags:**

| Flag | Meaning |
|---|---|
| `--name string` | New display name |
| `--prompt string` | New task prompt |
| `--rrule string` | Replace schedule with an RRule |
| `--cron string` | Replace schedule with a cron expression |
| `--timezone string` | IANA timezone |
| `--kind string` | Session kind |
| `--harness string` | Agent harness; empty uses project default |
| `--hook-secret string` | Replace a called automation's signing secret |
| `--hook-signature-header string` | Replace the signature header |
| `--filter string` | Replace the filters (repeatable) |
| `--dedupe-key string` | Replace the dedupe key template |
| `--enabled bool` | Enable or disable future dispatch |
| `--json` | Print JSON |

At least one update flag is required. A schedule cannot become a called automation, or the reverse. `--rrule` and `--cron` are mutually exclusive.

---

### ao automation delete

Delete an automation and its run history. Linked sessions remain available.

**Syntax:**
```text
ao automation delete <id> [--yes]
ao automation rm <id> [--yes]
```

Without `--yes`, type the automation id when prompted to confirm.

---

### ao automation runs

List run history for one automation.

**Syntax:**
```text
ao automation runs <id> [--limit <n>] [--cursor <cursor>] [--json]
```

`--limit` must be 1–100. When a non-JSON listing prints `next cursor: ...`, pass that value to `--cursor` for the next page.
