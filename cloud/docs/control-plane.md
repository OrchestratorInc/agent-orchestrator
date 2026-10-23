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
can rehydrate a captured conversation transcript and, with cloud artifacts on,
restores the session's artifact files. It restores preserved
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

## Session artifacts

With `AO_CLOUD_ARTIFACTS=1`, a session's deliverable files are kept outside
its sandbox, so they can be listed and opened while the sandbox is paused,
replaced, or archived. The flag applies to every organization; with it off the
artifact routes are not registered, `requestedOutput: "artifact"` is refused,
and no S3 client is built.

- **Worker.** When the launch context sets `artifacts` (artifacts are on and
  the worker advertises `session.artifacts`), the worker creates
  `<AO_DATA_DIR>/artifacts/<sessionId>` outside the checkout, passes it to the
  agent as `AO_SESSION_ARTIFACTS_DIR`, names it in the agent prompt, and adds it
  to the directories the harness may write (Claude `--add-dir`, Codex writable
  roots). It walks the directory through Go's rooted filesystem API, skipping
  symlinks, `.versions`, `.git` and `node_modules`, paths that do not fit a
  bucket key, and files over the limits, and uploads a file once two scans see
  the same size and modification time. The agent's Stop hook, a 30 s rescan, a
  2 s rescan after a change, and a final flush at shutdown trigger a sync. A
  failed request backs off from 10 s to 5 min; a request refused with a client
  error waits until the files change. The worker reports a deletion only for a
  file the same process synced or restored that is no longer in the directory
  at all.
- **Restore.** Before the agent starts, a worker on a new sandbox (after a
  restore or a replacement) lists the session's committed files with
  `GET /worker/artifacts` and downloads each one missing from the directory
  with `GET /worker/artifacts/blob/{path}`, which streams the row's
  `version_id`. Each file is written straight to its path in the artifact
  directory, created only if absent, so it never replaces a file the agent
  wrote, and is checked against the row's size and SHA-256; a download that
  fails or does not match is removed. A path that already exists keeps its
  local file, which counts as the committed file until a scan finds it
  changed. A restored or kept file counts as synced: it is not uploaded again
  unless it changes, and deleting it deletes the committed file. A restore
  that fails or runs past 30 s is retried in the background while the agent
  runs, skipping paths already synced, so a retry never brings back a file
  the agent deleted.
- **Storage.** The worker sends a manifest of `(path, size, sha256)`; the
  control plane answers with the paths whose committed row differs. Each
  changed file is relayed to the versioned tenant artifact bucket at
  `orgs/{orgId}/sessions/{sessionId}/{path}` with S3 verifying its SHA-256, so a
  mismatched body creates no version. Every upload is recorded in
  `ao_artifact_object_versions`; a session can hold at most 400 uncommitted
  uploads or 1000 MiB. Sandboxes hold no AWS credentials.
- **Commit.** The commit checks every uploaded `(path, versionId)` with
  `HeadObject`, then, in one transaction under the session row lock and only
  from the worker's current connection: claims each version as an open upload,
  upserts and deletes `ao_session_artifacts` rows, recomputes the session's
  `output_type`, queues the versions it replaced or deleted, and appends an
  `artifact.changed` event. A commit over 200 files or 500 MiB per session
  (25 MiB per file) changes nothing, and a commit that changes nothing appends
  no event.
- **Cleanup.** A sweeper in the control plane deletes, by version, every
  upload not committed within 24 hours and every replaced or deleted version
  one hour after its commit. A version the sweeper has claimed can no longer be
  committed.
- **User delete.** `DELETE /sessions/{id}/artifacts?path=...` deletes one file
  for a member of the session's organization. In one transaction it removes
  the row, queues its object version for collection now, updates the
  session's `output_type`, appends an `artifact.changed` event, and sends
  `ao_worker_work` with the payload `artifacts:{sessionId}`. The handler then
  deletes the stored version at once; the sweeper deletes it if that fails.
  Each replica remembers the change for ten minutes and wakes the worker's
  `GET /worker/work/wait`, which answers `artifactsChanged: true`. The worker
  then lists the committed files and removes each file it synced that is no
  longer committed, unless the file changed since it was synced; a changed
  file stays and is synced as a new one. The worker saves its synced files
  next to the artifact directory, so a worker that starts on a kept disk
  removes the files deleted while no worker ran before the agent starts, and
  a new sandbox never restores a deleted file.
- **Reads.** `GET /sessions/{id}/artifacts` reads PostgreSQL only and never
  wakes the sandbox; the session list carries only `artifactCount` and
  `artifactsUpdatedAt`. An orchestrator's `GET /worker/children` (`ao list`)
  carries each child's `artifactFiles` with the count and newest commit time.
  `POST /sessions/{id}/artifacts/access` returns a ten-minute path prefix
  signed for one user, organization, and session (an iframe cannot send a
  bearer header). `GET /artifact-files/{token}/{path}` checks that the user is
  still an active member of an active organization, reads the row, and streams
  the row's `version_id`, never the newest S3 version, with a sandbox
  Content-Security-Policy without `allow-same-origin` or popups,
  `connect-src 'none'`, `nosniff`, and an ETag of the SHA-256. A row whose
  object is gone returns 404 `artifact_unavailable`. A served page cannot
  fetch or open a window, and the desktop holds its frame to the session's
  access prefix, so it cannot carry its URL to another site; the token in that
  URL lasts ten minutes and works only for a current member. A frame that
  navigates with an expired link gets a short page saying so.

Every key comes from a worker's claims or from a row read under the caller's
organization; a client never supplies one. Paths are NFC-normalized and must be
relative, without `.` or `..` segments, backslashes, drive letters, control or
invisible format characters, Windows device names, or a leading `.versions` in
any letter case, with the whole key at most 1024 bytes.

Archiving a session keeps its artifacts, so a restored session gets them back;
a lifecycle policy that removes archived sessions' artifacts is tracked in
#7072. Deleting a project, which has no restore, deletes its sessions'
artifacts: in the same transaction every object version they own (committed,
replaced, or still uploading) is queued for the sweeper to delete now, the
artifact rows are removed, and the sessions' `output_type` keeps only their
pull requests. The sweeper deletes the objects on its next pass, within five
minutes. A commit for a session of a deleted project is refused, and deleting
the project again finishes the cleanup.

## Agent browser relay

The agent browser runs inside the sandbox; the control plane only relays it.
The worker holds a control connection on
`GET /api/cloud/v1/worker/agent-browser` and dials
`/api/cloud/v1/worker/agent-browser/streams/{id}` for each stream, one
HTTP/1.1 connection per stream. The desktop calls
`/api/cloud/v1/orgs/{orgId}/sessions/{sessionId}/agent-browser/{tail}`, which
is forwarded to the worker over those streams. Browser traffic is never
persisted. Like the live terminal relay, the relay is process-local: a request
is served only by the replica holding the worker's control connection, so the
deployment keeps one control-plane replica for this path.

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
