# ao automation

Manage durable session automations through the daemon API. An automation starts a new session on a schedule, or after each worker in its project ends.

## Syntax

```text
ao automation <subcommand> [args] [flags]
ao automations <subcommand> [args] [flags]
```

## Subcommands

---

### ao automation create

Create a scheduled or session-ended automation for a project.

**Syntax:**
```text
ao automation create --project <id> --name <name> --prompt <text> (--rrule <rule> | --cron <expr>) [flags]
ao automation create --project <id> --name <name> --prompt <text> --trigger session_ended [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project string` | Project id that owns the automation | Required |
| `--name string` | Display name | Required |
| `--prompt string` | Session task prompt | Required |
| `--trigger string` | `schedule`, or `session_ended` to run after each worker in the project is archived or its PR merges | `schedule` |
| `--rrule string` | RFC 5545 recurrence rule | Required for a schedule unless `--cron` is set |
| `--cron string` | Supported five-field cron expression | Required for a schedule unless `--rrule` is set |
| `--timezone string` | IANA timezone | Local zone when resolvable |
| `--kind string` | Session kind | `worker` |
| `--harness string` | Agent harness; empty uses project default | - |
| `--disabled` | Create disabled | - |
| `--json` | Print JSON | - |

**Example:**

```bash
ao automation create --project my-app --name "Morning triage" --prompt "Review overnight issues" --cron "0 9 * * *" --timezone America/Los_Angeles
ao automation create --project my-app --name "Session review" --prompt "Review how the session went and suggest improvements to our skills" --trigger session_ended
```

A session-ended run starts one new worker per ended worker. Its prompt ends with the ended session's ID, branch, PRs, task, and transcript path. Sessions started by a session-ended run never start another one.

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

Update automation metadata, schedule, harness, or enabled state.

**Syntax:**
```text
ao automation update <id> [flags]
```

**Flags:**

| Flag | Meaning |
|---|---|
| `--name string` | New display name |
| `--prompt string` | New task prompt |
| `--trigger string` | `schedule` (with `--rrule` or `--cron`) or `session_ended` |
| `--rrule string` | Replace schedule with an RRule |
| `--cron string` | Replace schedule with a cron expression |
| `--timezone string` | IANA timezone |
| `--kind string` | Session kind |
| `--harness string` | Agent harness; empty uses project default |
| `--enabled bool` | Enable or disable future dispatch |
| `--json` | Print JSON |

At least one update flag is required. `--rrule` and `--cron` are mutually exclusive.

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
