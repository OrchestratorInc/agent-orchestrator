# ao review

Manage AO's native code reviewer for a worker's PR.

AO's reviewer is an isolated reviewer agent, not another worker session. To get
an adversarial review of an AO session's PR, use `ao review trigger`; never
spawn a worker to review it. Inside a worker, the review commands default to
the calling session (`AO_SESSION_ID`), so `ao review trigger` reviews your own
PR. An orchestrator passes the worker's session id.

AO's review is internal. An AO approval is not a GitHub approval: it does not
satisfy required or independent-account reviews or branch protection, and it
does not authorize merging.

## Syntax

```
ao review <subcommand> [args] [flags]
```

The review loop can be inspected, submitted, cancelled, triggered again, and its
findings resolved.

AO's reviewer files each change it requires as a **finding in AO**, not as a
GitHub review thread. AO posts one summary comment on the PR, then delivers the
verdict and the open findings, with their ids, to the worker once. The worker
resolves each finding in AO with `ao review resolve` and does not reply on
GitHub for them. Review comments from people and bots on GitHub are still
forwarded as before; a worker's own replies on those threads are not forwarded
back to it.

## Subcommands

---

### ao review ls

List review runs for a worker session (default: the calling session). Alias: `list`.

**Syntax:**
```
ao review ls [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output reviews as JSON | - |

The table lists each PR's review state; below it, every open finding with its id.

**Example:**

```bash
ao review ls mer-3
```

---

### ao review submit

Record a reviewer's result for a worker's PR.

**Syntax:**
```
ao review submit [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--body string` | Review body: a path to a Markdown file, or `-` to read from stdin | - |
| `--review-id string` | Id of a GitHub PR review the reviewer posted itself. Only reviewers started before findings moved into AO pass it | - |
| `--reviews string` | JSON review results array or object: a path, or `-` to read from stdin. Each result takes `runId`, `verdict`, `body`, and `findings` (`[{"path", "line", "body"}]`) | - |
| `--run string` | Review run id | Required |
| `--session string` | Worker session id (or pass it as the positional argument) | - |
| `--verdict string` | Review verdict: `approved` or `changes_requested` | Required |

A `changes_requested` result needs at least one finding, and an approval has
none (optional suggestions go in `body`). AO stores the findings with the
verdict in one step, so retrying the same submission records nothing twice.

If the local daemon is restarting when a result is submitted, AO retains the
parsed result in memory and retries the same idempotent request for up to 30
seconds. Validation errors return immediately. If the daemon remains unavailable,
the command reports failure and can be repeated safely; daemon idempotency
handles the case where an earlier connection dropped after committing the result.

**Examples:**

```bash
# Submit an approved review for session mer-3
ao review submit mer-3 --run review-run-1 --verdict approved
```

```bash
# Submit a changes-requested review with its findings from stdin
printf '%s' '{ "reviews": [ { "runId": "review-run-1", "verdict": "changes_requested", "body": "One correctness issue.", "findings": [ { "path": "src/auth.go", "line": 42, "body": "Check the token for nil before use." } ] } ] }' | ao review submit --session mer-3 --reviews -
```

---

### ao review resolve

Resolve one or more AO review findings with a note on how each was handled.
The worker resolves its own findings (default: the calling session); an
orchestrator passes `--session <worker>`, and only on a human's instruction.
Reviewer panes cannot run it. A finding that a newer review replaced cannot be
resolved, and resolving an already resolved finding changes nothing.

**Syntax:**
```
ao review resolve <finding-id>... [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--note string` | What changed, or why no change is needed | - |
| `--session string` | Worker session that owns the findings | The calling AO session |

**Example:**

```bash
ao review resolve 6f1c0d2e-... --note "Added the nil check and a test."
```

---

### ao review cancel

Cancel every running review for a worker's PR (default: the calling session). Alias: `stop`.

**Syntax:**
```
ao review cancel [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Worker session id (or pass it positionally) | - |

**Example:**

```bash
ao review cancel mer-3
```

---

### ao review trigger

Start AO's reviewer on a worker's open PR heads (default: the calling session).
Aliases: `execute`, `restart`. Reviewer panes cannot run it.

**Syntax:**
```
ao review trigger [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Worker session id (or pass it positionally) | The calling AO session |
| `--pr string` | Review only this PR URL, attaching it to the session first if AO does not track it yet (never from another active session) | Every eligible PR on the session |
| `--agent string` | Reviewer agent for this pass only (alias `--harness`) | Session reviewer, then project reviewer, then the project's default worker agent |
| `--model string` | Reviewer model for this pass only | As above |
| `--effort string` | Reviewer reasoning effort for this pass only | As above |
| `--rerun` | Review a head that already has a review again, or add a reviewer with a different agent alongside one that is still running | - |
| `--no-inject` | Leave the session's review auto-inject setting unchanged | Auto-inject is turned on |

AO fetches the session's PRs fresh from the provider before deciding what is
due, so the pass covers the commit really on the PR. Right after opening a PR,
pass `--pr <url>`.

A head that is already being reviewed, or already has a review, is not reviewed
again: the command exits 1 with `REVIEW_ALREADY_RUNNING` or
`REVIEW_HEAD_ALREADY_REVIEWED` and says what to do. Push new commits, or pass
`--rerun`. The trigger re-reads the PR from the provider first; if you
just pushed and it still says already reviewed, wait a few seconds and retry. The same reviewer agent never runs twice on one head at the same time.

By default the command turns on the worker session's review auto-inject, so AO
delivers each finished pass to the worker once: the verdict, and for requested
changes every open finding with its id. A pass that finishes while the worker
is waiting for input is delivered when it can take messages again. A newer
completed review of the same PR replaces the findings still open from earlier
passes. If a run shows `failed` or `cancelled`, trigger again; nothing else
reports it.

**Examples:**

```bash
# Inside a worker: request a review of this session's PR once it is ready
ao review trigger
```

```bash
# Orchestrator: review a worker's PR with a specific reviewer
ao review trigger mer-3 --agent codex --model gpt-5.5
```

```bash
# Add a second opinion on the same commit while the first reviewer runs
ao review trigger --agent claude-code --rerun
```
