# AO Cloud support runbook

How to go from "a customer says it's broken" to the cause, quickly. The path is
always the same:

1. Email to user, organizations and sessions (support lookup).
2. The organization's **activity trail**: what happened, in order.
3. **CloudWatch Logs Insights** for the detail around any event.
4. For bring-your-own (BYO) Coder: the session's `startupError.detail`, then
   the customer's Coder.

Nothing here logs or stores tokens, prompts, repository contents or emails in
logs. Identifiers (user, org, session, connection ids) are opaque UUIDs.

## 1. Find the customer: support lookup

The control plane serves an operator-only, read-only lookup. It is enabled by
setting `AO_CLOUD_SUPPORT_TOKEN` (at least 32 characters) on the control plane;
without it the route does not exist. Keep the token in Secrets Manager next to
the other control-plane secrets (`ao-cloud/<env>/...`) and never share it with
customers.

```bash
export AO_CLOUD_URL=https://api.aoagents.dev            # staging: your staging CP URL
export AO_CLOUD_SUPPORT_TOKEN=...                      # from Secrets Manager

# By email (case-insensitive): every account, its orgs, recent sessions, activity.
curl -sS -H "Authorization: Bearer $AO_CLOUD_SUPPORT_TOKEN" \
  "$AO_CLOUD_URL/api/cloud/v1/control/support/lookup?email=dev@example.com" | jq

# By organization or by session (the session's org, filtered to that session).
curl -sS -H "Authorization: Bearer $AO_CLOUD_SUPPORT_TOKEN" \
  "$AO_CLOUD_URL/api/cloud/v1/control/support/lookup?orgId=<ORG_ID>&sessions=50&activity=300" | jq
curl -sS -H "Authorization: Bearer $AO_CLOUD_SUPPORT_TOKEN" \
  "$AO_CLOUD_URL/api/cloud/v1/control/support/lookup?sessionId=<SESSION_ID>" | jq
```

The report contains, per organization: kind, plan and subscription status, the
BYO Coder connection (id, validation state), the newest sessions with their
sandbox state (`provider`, `providerEnvironmentId` = Coder workspace id or
Freestyle VM id, `connectionId`, `desiredState`/`observedState`,
`workerLastSeenAt`, `startupAttempts`, `startupErrorCode`/`Message`/`Detail`,
`lastError`) and the newest activity events.

Useful one-liners:

```bash
# Sessions that never became ready, with their startup error.
... | jq '.users[].orgs[].sessions[] | select(.workerLastSeenAt == null)
          | {sessionId, provider, observedState, startupErrorCode, startupErrorDetail}'
# The trail, oldest first, compact.
... | jq -r '.users[].orgs[].activity | reverse[] | "\(.createdAt) \(.kind) \(.provider) \(.attrs)"'
```

### SQL fallback

When the endpoint is unavailable, run the same reads through the one-off
Fargate DB task (see the DB access notes). Every tenant table has **forced
RLS**: a raw `SELECT` sees zero rows unless the transaction carries an
identity. Set it per query block:

```sql
BEGIN;
-- ao_users has no RLS.
SELECT id, auth_provider, email, created_at FROM ao_users WHERE lower(email) = lower('dev@example.com');

-- Memberships are visible under the user's identity.
SELECT set_config('ao.user_id', '<USER_ID>', true);
SELECT m.org_id, m.role, o.slug, o.kind, o.plan, o.subscription_status
FROM ao_org_memberships m JOIN ao_organizations o ON o.id = m.org_id
WHERE m.user_id = '<USER_ID>';

-- Sessions, sandboxes and the trail are visible under the org's identity.
SELECT set_config('ao.org_id', '<ORG_ID>', true);
SELECT s.id, s.kind, s.harness, s.created_at, b.provider, b.provider_environment_id,
       b.provider_connection_id, b.desired_state, b.observed_state, b.worker_last_seen_at,
       b.startup_attempts, b.startup_error_code, b.startup_error_message, b.startup_error_detail, b.last_error
FROM ao_sessions s LEFT JOIN ao_sandboxes b ON b.session_id = s.id
WHERE s.org_id = '<ORG_ID>' ORDER BY s.created_at DESC LIMIT 20;

SELECT created_at, kind, provider, session_id, attrs
FROM ao_activity_events WHERE org_id = '<ORG_ID>'
ORDER BY created_at DESC LIMIT 200;
ROLLBACK;
```

## 2. The activity trail (`ao_activity_events`)

One row per notable event, per organization, kept 90 days. Each row is also
logged as `msg = "activity event"` with `activity_kind` and its attributes under
`activity.*`, so the same events drive CloudWatch metrics and queries.

| kind | when | key attrs |
|---|---|---|
| `org.created` | first sign-in creates an org (signup) | `org_kind` personal/team, `auth_provider` |
| `member.joined` | a user's first sign-in into a team org | `role`, `first_member` |
| `project.created` | cloud project created | `project_id` |
| `session.create_requested` | session accepted | `project_id`, `session_kind`, `harness`, `connection_id`, `project_snapshot` |
| `session.create_refused` | refused before creation | `code` (e.g. `coder_template_required`) |
| `billing.plan_refused` | no plan | `code` `PLAN_REQUIRED`, `route` |
| `billing.limit_hit` | slot or usage limit (request or enforcer pause) | `code`, `plan`, `limit`, `used`, `resets_at`, `source` |
| `billing.checkout_started` / `_completed` | Stripe Checkout | `plan` |
| `billing.plan_changed` | webhook applied a subscription | `plan`, `subscription_status` |
| `sandbox.provisioned` | provider created compute | `duration_ms`, `provider_environment_id` |
| `bootstrap.stage` | a provider bootstrap stage finished (Coder) | `stage`, `ok`, `duration_ms`, `workspace_id`, `coder_owner`, `coder_template`, `agent_lifecycle`, `code`, `error` |
| `worker.connected` | a worker registered | `worker_id`, `epoch`, `worker_version` |
| `sandbox.ready` | worker up (time-to-ready) | `phase` create/resume, `ttr_ms`, `startup_attempts` |
| `session.start_failed` | a new startup error for the session | `code`, `message`, `stage`, `cause`, `customer_owned`, `job_error`, `agent_lifecycle`, `build_log_tail`, `agent_log_tail` |
| `sandbox.paused` / `sandbox.resuming` | compute stopped / restore started | `desired_state`, `reason` |
| `session.resume_requested` | user resumed a session | `observed_state` |
| `worker.disconnected` | heartbeat timeout repair | `reason`, `last_seen_ago_ms` |
| `worker.relaunched` | a launched worker never checked in within its check-in window and was relaunched with a fresh ticket (once per startup) | `reason` `never_checked_in`, `epoch` (the abandoned launch), `launched_ago_ms`, `redeemed`, `checkin_timeout_ms`, `startup_attempts` |
| `sandbox.terminated` | startup ceiling parked the session | `code`, `ceiling_ms`, `customer_owned` |
| `worker.git_grant_failed` | worker could not get repo credentials | `code`, `error` |
| `coder.connection_validated` | BYO Coder connection saved/tested | `result` ok/rejected, `reason`, `error`, `coder_host`, `connection_id` |

## 3. CloudWatch Logs Insights

Log group: `/ao-cloud/<env>/control-plane` (JSON lines). Fields present on
every line logged with a request or reconcile context:

`request_id`, `user_id`, `org_id`, `session_id`, `sandbox_provider`,
`connection_id` (BYO Coder connection), `worker_id`. Access-log lines are
`msg = "HTTP request complete"` with `method`, `route`, `status`,
`duration_ms`. Activity lines are `msg = "activity event"` with
`activity_kind` and `activity.*`. Coder stages are `msg = "provider stage
complete"` / `"provider stage failed"` with `stage`, `duration_ms`,
`workspace_id`, `coder_owner`, `coder_template`, `agent_lifecycle`, `err`.

Everything one user did (requests, the reconciler work their sessions caused
is keyed by session/org, see the next queries):

```
fields @timestamp, level, msg, method, route, status, org_id, session_id, sandbox_provider, activity_kind, error, err
| filter user_id = "<USER_ID>"
| sort @timestamp desc
| limit 500
```

Everything for one organization, errors first:

```
fields @timestamp, level, msg, route, status, user_id, session_id, sandbox_provider, connection_id, activity_kind, error, err
| filter org_id = "<ORG_ID>"
| sort @timestamp desc
| limit 500
```

One session's whole lifecycle (API calls, reconciler, stages, worker transport):

```
fields @timestamp, level, msg, route, status, stage, duration_ms, activity_kind, workspace_id, agent_lifecycle, startup_error_code, err, error
| filter session_id = "<SESSION_ID>"
| sort @timestamp asc
| limit 1000
```

One request (from the `X-Request-ID` response header or an error body's `requestId`):

```
fields @timestamp, level, msg, route, status, duration_ms, user_id, org_id, session_id, error
| filter request_id = "<REQUEST_ID>"
| sort @timestamp asc
```

Signup and checkout funnel per day:

```
filter msg = "activity event" and activity_kind in ["org.created", "project.created", "session.create_requested", "sandbox.ready", "billing.checkout_started", "billing.checkout_completed", "billing.plan_refused", "billing.limit_hit"]
| stats count(*) as events by activity_kind, bin(1d) as day
| sort day desc
```

Time-to-ready p50/p95 per provider (first start):

```
filter activity_kind = "sandbox.ready" and activity.phase = "create"
| stats count(*) as ready, pct(activity.ttr_ms, 50) as p50_ms, pct(activity.ttr_ms, 95) as p95_ms, max(activity.ttr_ms) as max_ms by sandbox_provider
```

Session start outcomes per provider (success rate = ready / requested):

```
filter activity_kind in ["session.create_requested", "sandbox.ready", "session.start_failed"]
| filter activity_kind != "sandbox.ready" or activity.phase = "create"
| stats count(*) as events by sandbox_provider, activity_kind
| sort sandbox_provider asc
```

Start failures by code and stage:

```
filter activity_kind = "session.start_failed"
| stats count(*) as failures, count_distinct(org_id) as orgs by sandbox_provider, activity.code, activity.stage
| sort failures desc
```

BYO Coder bootstrap failures with their detail:

```
fields @timestamp, org_id, session_id, connection_id, activity.code, activity.stage, activity.job_error, activity.agent_lifecycle, activity.cause, activity.agent_log_tail
| filter activity_kind = "session.start_failed" and ispresent(connection_id)
| sort @timestamp desc
| limit 50
```

Coder bootstrap stage durations and failure rates:

```
filter msg in ["provider stage complete", "provider stage failed"]
| fields strcontains(msg, "failed") as failed
| stats count(*) as runs, sum(failed) as failures, pct(duration_ms, 50) as p50_ms, pct(duration_ms, 95) as p95_ms by stage
```

Add `| filter ispresent(connection_id)` after the first line for BYO only.

Freestyle provider errors:

```
fields @timestamp, level, msg, org_id, session_id, provider_id, err, error
| filter sandbox_provider = "freestyle" and level in ["WARN", "ERROR"]
| sort @timestamp desc
| limit 200
```

Stripe webhook and API failures:

```
fields @timestamp, msg, step, event_type, event_id, error, request_id
| filter msg in ["Stripe webhook failed", "Stripe request failed"]
| sort @timestamp desc
```

Server errors by route:

```
filter msg = "HTTP request complete" and status >= 500
| stats count(*) as errors by route, status
| sort errors desc
```

Activity trail health (should be empty):

```
filter msg in ["activity queue full; dropping events", "record activity event", "activity event discarded"]
| stats count(*) by msg, bin(1h)
```

Dashboards and alarms for these metrics are defined in
`cloud/scripts/lib/offering_monitoring.py` (rendered JSON in
`cloud/infra/monitoring/`); apply with
`cloud/scripts/configure-offering-monitoring.py --environment <env> --apply`
(or `--print-commands` to review the aws CLI calls).

## 4. BYO Coder (customer-owned Coder)

A BYO session runs on the customer's Coder through their connection
(`ao_provider_connections`, provider `coder`). AO never stops or deletes a BYO
workspace on failure; it parks the session and leaves the workspace running so
it can be inspected.

### Read the startup error

`GET /api/cloud/v1/orgs/<ORG_ID>/sessions/<SESSION_ID>` (or the support
lookup) returns `startupError {code, message, at, detail}`. `detail` is only
present for BYO compute, redacted and at most 4 KB:

```
code: bootstrap_failed
stage: worker_install
cause: coder: launch preinstalled worker: coder: worker bootstrap failed: AO worker did not start ...
agent_lifecycle: start_error
build_status: running
coder_owner: <coder user>
coder_template: <template id>
workspace_id: <workspace id>
--- build log (tail) ---
...
--- agent startup log (tail) ---
...
```

`stage` is where it failed: `workspace_create`, `workspace_start`,
`agent_wait`, `durable_root_probe`, `ready_probe` (the connection's
`readyProbe`, run as the workspace user until it exits 0; attrs carry
`attempts` and `exit_code`), `worker_install` (installs and launches
the worker with `setsid`; the `cause` shows the last 1 KB of bootstrap
terminal output; attrs carry `worker_user` and `checkout_reuse`). A failure
after `worker_install` succeeded (no heartbeat, `worker_never_started`) is on
the worker side: read its log. AO relaunches such a worker once, with a fresh
bootstrap ticket, when its check-in window (`AO_CLOUD_WORKER_CHECKIN_TIMEOUT`,
default 12 minutes from the launch) closes; a `worker.relaunched` event marks
it, and the session parks when the relaunched worker's window closes too. With `reuseExistingCheckout`, the worker log
says which checkout it adopted (`checkout reuse: ...`), any stash it made for
uncommitted changes, or why it cloned into `<root>/repository` instead.

### Look at the workspace in the customer's Coder

With a token for the customer's Coder (they run these, or give you access):

```bash
export CODER_URL=https://coder.customer.example CODER_SESSION_TOKEN=...
coder list --all                                  # find ao-<session prefix> workspaces
coder show <owner>/<workspace>                    # build status, agent lifecycle, resources
coder ssh <owner>/<workspace> -- tail -n 200 '$HOME/.ao/worker/worker.log'

# Build log and agent startup-script log through the API (what AO captures):
curl -sS -H "Coder-Session-Token: $CODER_SESSION_TOKEN" "$CODER_URL/api/v2/workspaces/<workspace id>" \
  | jq '{build: .latest_build.id, status: .latest_build.status, job: .latest_build.job,
         agents: [.latest_build.resources[].agents[]? | {id, status, lifecycle_state, architecture}]}'
curl -sS -H "Coder-Session-Token: $CODER_SESSION_TOKEN" "$CODER_URL/api/v2/workspacebuilds/<build id>/logs" | jq -r '.[].output' | tail -50
curl -sS -H "Coder-Session-Token: $CODER_SESSION_TOKEN" "$CODER_URL/api/v2/workspaceagents/<agent id>/logs" | jq -r '.[].output' | tail -50
```

Worker files on the workspace live under the durable root (default `$HOME`,
or the connection's `durableRoot`): `<root>/.ao/worker/worker.log` (worker
stdout/stderr, JSON), `worker.pid`, `launch.sh`; the repository is
`<root>/repository`. Freestyle and CreateOS workers log to
`/var/log/ao-worker.log`.

### Common failure codes

| code | what it means | fix |
|---|---|---|
| `workspace_not_ready` | the Coder agent never reached `ready` (still `starting`, or the build is stuck) | read the agent startup log tail; a slow template startup script needs a larger `startupTimeoutSeconds` on the connection |
| `workspace_build_failed` | the Coder workspace build failed or was canceled (template error, cloud quota such as AWS `VcpuLimitExceeded`); reported once per failed build, as soon as Coder ends it. A bring-your-own workspace's message is the build log's redacted `Error:` line; an AO-managed one shows only the cloud error code (for example `VcpuLimitExceeded (cloud capacity limit)`), with the line in the CP log (`sandbox workspace build failed`) and the startup error detail | read `job_error` and the build log tail; fix the template or free/raise the quota, then Retry: it starts one new build (managed workspaces were stopped and are started again; bring-your-own ones are left as they are until the retry). AO never starts a build on its own for a failed build seen while a worker is connected, or for a failed stop/delete |
| `workspace_create_failed` | AO could not create the workspace (Coder API refused it, connection unusable, or no provider capacity) within the startup budget; the session is parked at the startup ceiling. The message is generic; the provider's response is in the detail (bring-your-own) and the CP log | fix the connection/template, then Retry |
| `terminal_unavailable` | AO could not open a PTY on the agent (agent disconnected or reconnecting) | `coder ping <workspace>`; check the agent's network path to Coder (DERP/relay), restart the workspace |
| `unsupported_architecture` | the workspace CPU has no AO worker build | AO ships amd64 and arm64; for arm64 make sure the control-plane image includes `ao-worker-linux-arm64` (the CP logs "arm64 worker build unavailable" at boot if not) |
| `durable_root_unavailable` | the durable root is not usable: `$HOME` not writable or a symlink, not a mount when `requireMountedDurableRoot` is on, or it holds another session's data | fix ownership/permissions of the home directory or mount, or turn off `requireMountedDurableRoot`; a reused volume from another session needs a fresh workspace |
| `bootstrap_failed` | the bootstrap script failed: no passwordless sudo, or the worker exited right after launch | give the workspace user passwordless sudo; otherwise read `cause` and `worker.log` (common: no egress to the control plane URL, TLS interception, disk full) |
| `workspace_setup_not_ready` | the connection's `readyProbe` never exited 0 within `readyProbeTimeoutSeconds` (measured from the agent becoming ready); the message carries the probe's last output (bounded, redacted) | run the probe yourself in the workspace as the workspace user; fix the template's own setup or raise the timeout (30..3600 s) |
| `worker_never_started` | the worker launched but never sent a heartbeat before the deadline | `worker.log` on the workspace; check egress to `AO_CONTROL_PLANE_URL`, proxies, clock skew |

Other BYO signals on the trail:

* `coder.connection_validated` with `result=rejected`: `reason=invalid_config`
  (bad URL or token shape) or `coder_unreachable` (the control plane could not
  call `/api/v2/users/me`: wrong URL, expired token, or the Coder is private
  and needs the PrivateLink endpoint).
* `session.create_refused code=coder_template_required`: the project has no
  Coder template picked and the org has no default. Pick one on the project.
* `worker.git_grant_failed code=CHECKOUT_NOT_AUTHORIZED`: the GitHub App is not
  installed on the repository (or a stored PAT was revoked); the checkout and
  git credential helper fail until it is.

Reconciler log lines for a BYO session all carry `connection_id`, so

```
filter connection_id = "<CONNECTION_ID>"
| fields @timestamp, level, msg, session_id, stage, duration_ms, agent_lifecycle, err
| sort @timestamp desc
```

shows every session on that customer's Coder at once.
