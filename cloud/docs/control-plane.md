# Control-plane state and cluster behavior

PostgreSQL stores identities, memberships, projects, session/workspace intent,
commands, turns, events, and audit records. Ordinary API requests can use any
healthy replica. Live terminal relay connections stay in process memory; see
[Worker workspace and terminal transport](#worker-workspace-and-terminal-transport)
for the recovery path and deployment constraint.

## Durable session creation

Creating a session commits one PostgreSQL transaction containing:

1. an idempotency command receipt;
2. the session and generated branch;
3. the mode and denied-command security policy;
4. a one-to-one sandbox row with `desired_state = running` and
   `observed_state = requested`;
5. the initial prompt event and queued turn, when a prompt was supplied;
6. an audit event; and
7. the completed command result.

The sandbox row is desired-state intent only. The request handler does not
provision a worker inline. A reconciler claims requested sandboxes and updates
their observed state through the configured provider without changing the
client-facing creation flow.

`AO_CLOUD_SANDBOX_PROVIDER` selects the default provider recorded on new
sandboxes. An explicit provider connection, when supplied, determines the
credential but cannot change that provider: its provider must match the
sandbox provider. Agent credentials and sandbox-provider credentials are not
interchangeable.

## Durable messages and replay

Sending a message also commits atomically:

- an idempotency command receipt;
- a gap-free sequence allocated from the session row;
- a `chat.user_message` event;
- one queued turn; and
- a content-free audit record containing only the event sequence.

Only one unfinished turn is permitted per session by a database constraint.
Retries with the same idempotency key return the original event. Reusing a key
for different input returns a conflict.

`GET .../chat-events` replays committed client events after a sequence.
`GET .../events` provides server-sent events by replaying and polling
PostgreSQL. It keeps no replica-local subscription state, so reconnecting to a
different replica is safe: the client resumes from its last sequence. The
polling implementation is intentionally simple for the first deployment; a
database notification or broker may later reduce polling latency without
changing event durability.

The shared Cloud client tracks the highest consumed sequence and reconnects
retryable stream failures with a fresh access token. Duplicate sequences are
suppressed. Cancellation and non-retryable client errors stop the stream.

## Sandbox resume and lifecycle projection

Provider-native idle stops are normal lifecycle observations. When a provider
positively identifies an idle/autostop transition, reconciliation atomically
accepts it as `desiredState=paused` and `observedState=stopped`; it does not
immediately restart the workspace. `POST
.../sessions/{sessionId}/resume`, a new user message, and a user-requested
workspace operation record per-session resume intent. Project/session listing
does not resume a sandbox. Requesting a terminal ticket does: it changes a
paused sandbox to running and normally renews its short interaction lease
before checking whether the worker is ready. Repeated unconsumed tickets can
suppress lease renewal, but they still permit a paused sandbox to wake. A
terminal retry can therefore restart compute even before attachment succeeds.

Session list and detail responses expose `sandboxProvider`, `desiredState`, and
`observedState` alongside `runtimeConnected`, `runtimeState`, and
`runtimeError`. The desktop derives paused/resume progress from those durable
intent and observation fields. A terminal ticket requests activity; it does not
prove that the provider has finished resuming or that the worker is connected.

Archiving a Cloud session marks it terminated and sets the sandbox's desired
state to deleted. The reconciler tears down the provider environment and keeps
the session and event history. Restore changes the desired state back to
running. After teardown completes, it provisions a new environment. The worker
can rehydrate a captured conversation transcript. It restores preserved
uncommitted work only when the checkpoint includes a preserved Git ref. The provider's own storage and retention rules still apply, so code
that must survive a teardown should be committed and pushed.

The server also enforces a configurable concurrent-sandbox limit per
organization. The implementation default is `1000` when a deployment does not
set another value. This is an operational default, not a customer pricing or
entitlement statement. Provider capacity can reject a create request before
the Cloud limit is reached. The API reports an organization limit as
`SANDBOX_QUOTA_EXCEEDED`.

## Worker workspace and terminal transport

Workspace file operations and workspace-terminal traffic never touch a
control-plane filesystem. An authenticated tenant request is committed to
`ao_worker_requests` with the session's current worker epoch. The session
worker polls and leases that command using its JWT identity, executes it under
`AO_WORKSPACE_DIR`, and commits the bounded result. The originating request
polls PostgreSQL, so submission, worker claim, and result delivery can pass
through different control-plane replicas.

Requests expire, leases can be reclaimed, and each session has a durable
concurrency cap. Disconnecting or timing out a workspace request marks its
command cancelled. Worker replacement fences claims, completions, terminal
input, and terminal output from every older epoch.

Workspace paths are opened through Go's rooted filesystem API. Absolute paths,
traversal, and symlinks escaping the workspace are rejected. Files must be
regular UTF-8 files and are limited to 1 MiB; listings, diffs, response bodies,
terminal frames, terminal output history, operation duration, and concurrent
requests are also bounded.

Terminal tickets are random, hashed at rest, short-lived, single-use, and bound
to a session epoch. Requesting a ticket can wake the sandbox as described above.
An attached terminal also renews the interaction lease periodically, even
without input. Keeping a terminal connection open can delay idle shutdown.

With `AO_CLOUD_TERMINAL_RELAY=1` and terminal streaming enabled, terminal input
uses the process-local worker stream when available. If that stream is absent
or saturated, input becomes a durable worker request. The relay sends worker
output to an attached client first, then mirrors that frame to PostgreSQL in
order. Output can replay from PostgreSQL by sequence. Durable storage remains the recovery
source, and the queue path is the fallback when no local relay stream exists.
The live relay uses process-local connections; the deployment runbook keeps one
control-plane replica for that path.

The API accepts `kind=workspace` for a workspace shell and `kind=agent` for the
coding agent's native terminal. Agent attachment requires a live agent terminal;
a finished agent terminal cannot be reopened by retrying a ticket. Interface
transitions and worker readiness determine when that terminal is available.

Terminal access separates observation from operation:

- Authorized users can receive `terminal:read`, including viewers and users
  whose effective mode or denied-command policy prevents input.
- `terminal:operate` requires a role other than viewer, an effective mode other
  than read-only, and no effective denied commands.
- Operating a workspace shell also requires trusted mode. An agent terminal
  does not have that extra trusted-mode requirement.

The Cloud service applies project-share restrictions when calculating the effective
mode and denied commands. A read ticket does not grant permission to send
terminal input. The desktop connects to these public API endpoints; terminal
support in the optional private web app must be checked in that app's source
and deployment.

## Replica lifecycle

- `/healthz` reports process liveness.
- `/readyz` checks PostgreSQL and returns unavailable while the process is
  draining.
- Both successful probes report `environment` and `release`, and every response
  includes `X-AO-Release`, so load-balancer checks and rollout debugging can
  identify the exact image revision serving traffic.
- On shutdown, the process marks itself draining before gracefully closing the
  HTTP server. Active event streams observe the drain signal and exit instead
  of holding shutdown open.
- Long-lived event streams have no global HTTP write timeout. Request bodies
  remain bounded, and server read/header/idle timeouts remain enabled.
- Production startup rejects a runtime database role with `SUPERUSER` or
  `BYPASSRLS`. `AO_CLOUD_MIGRATION_DATABASE_URL` may hold a separate elevated
  migration credential; ordinary requests only use `AO_CLOUD_DATABASE_URL`.

Authentication caches and development rate-limit counters are replica-local.
Authorization and durable product state are checked against PostgreSQL. The
process-local terminal relay has the separate deployment constraint described
above; this persistence model is not a claim that every live connection can
move freely between replicas.

## Version and environment boundaries

- The public HTTP contract is versioned under `/api/cloud/v1`.
- PostgreSQL changes are ordered Goose migrations and are never inferred from
  the running binary.
- `AO_CLOUD_RELEASE` identifies the deployed image, normally with an immutable
  Git SHA or release tag. It is required in `staging` and `production`.
- `staging` and `production` are both hosted modes: local authentication is
  rejected and the runtime database role must not bypass RLS.

Separate staging/production ECS services, ALB target groups, Secrets Manager
paths, RDS databases, image promotion, canary percentages, and rollback rules
belong to deployment infrastructure rather than application branching.

## GitHub webhook automation

When the GitHub App is configured, its webhook URL is:

```text
https://<cloud-public-host>/api/cloud/v1/github/webhooks
```

Use the same secret as `AO_CLOUD_GITHUB_WEBHOOK_SECRET` and subscribe to
`Pull requests`, `Check suites`, `Check runs`, and `Pull request reviews`.
GitHub deliveries are signature-verified, deduplicated, and persisted. Installation
routing changes retain receipt order; SCM deliveries process independently so a
retrying PR cannot block later updates. Verified PR-opened events match the
session branch or a branch head reported by its worker. A worker report also
replays an earlier verified PR-opened delivery when the webhook arrived first.
They update durable pull-request facts and Cloud notifications. Failing CI is
sent to the session worker only when that session has automatic CI feedback
enabled.

For local testing, expose the control plane through a temporary public HTTPS
tunnel and use the tunnel URL above. The tunnel is test-only; production uses
`AO_CLOUD_PUBLIC_URL`. GitHub App configuration is accepted only when the
control plane runs with `AO_CLOUD_ENVIRONMENT=production`, including a local
end-to-end webhook test.

PR status changes are refreshed from GitHub webhook deliveries. Failed delivery
processing is retried from the durable webhook queue; the control plane does
not periodically refresh open PRs from GitHub. GitHub may initially report
mergeability as unknown while it computes the result. Without another relevant
webhook, that value stays unknown until the next PR action triggers an update.
