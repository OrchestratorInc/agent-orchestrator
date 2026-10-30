# Product telemetry

Orchestrator.inc collects limited product-usage and reliability data to learn which parts of
the app are useful and whether releases are working as expected. If you sign in
to AO Cloud, Orchestrator.inc sends the email address of that account to PostHog
as a person property at sign-in and subsequent signed-in launches, along with
your AO Cloud user ID and GitHub username.
Without an AO Cloud sign-in, no email address is sent. Some events include a
GitHub project owner or an authenticated username, so they can identify an
account. Desktop and daemon events include a random installation identifier
to group activity even when the event uses an account ID as `distinct_id`.
Telemetry is not wholly anonymous.

Remote telemetry is enabled in production desktop and mobile releases. A
packaged desktop release also enables telemetry for the daemon it starts.
Development builds and daemons started directly do not send remote telemetry by
default.

## Sentry projects

The packaged desktop app gives its spawned Go daemon the daemon Sentry project
DSN through `AO_SENTRY_DSN`. An explicit `AO_SENTRY_DSN` value takes precedence;
without one, development builds hand the daemon an empty value. Daemon error
reporting remains subject to the existing Share error events control.

Electron main and the renderer have a separate desktop Sentry project. Their
default DSN is distinct from the daemon project; Electron main uses
`AO_ELECTRON_SENTRY_DSN` for an explicit override. Without one, development
builds use an empty Electron DSN. The renderer uses this main-owned sender
and has no independent DSN override. The production release gate remains
closed until the privacy policy names Sentry and its data categories.
Once that gate opens, Electron main sends consented error events through its
own Sentry transport.
Renderer diagnostics pass through the existing main-owned consent boundary;
the renderer has no direct Sentry transport. The mobile app keeps its own
Sentry project and DSN. A desktop opt-in recorded while the Electron release
gate was closed requires a fresh opt-in before this sender starts.
Once enabled, renderer exceptions go to Electron Sentry rather than PostHog. Failed
Sentry sends can wait in an offline queue under AO's Electron data directory.
Error events can include a standard exception type and allowlisted in-app
function names and line numbers. Stack paths and raw exception messages are
removed before sending.
Turning off Share error events drains the sender and removes that queue before
the opt-out is complete. If the app exits during that cleanup, startup purges
the leftover queue while sharing is off, before a later opt-in can start the
sender.

## What Orchestrator.inc sends

Orchestrator.inc sends structured events in a few broad categories:

- App usage, such as launching Orchestrator.inc, viewing a coarse area of the interface, or
  starting a task or agent session
- Feature outcomes, such as whether creating a project, starting an agent,
  creating an automation, connecting the mobile app, or installing an update succeeded
- The GitHub organization or account that owns a project's configured remote,
  recorded on project-add events. Only the owner segment is sent, never the
  repository name, path, or URL. For a personal repository this owner is the
  user's own GitHub username, so this particular value is not anonymous. We use
  it to understand which organizations and developers get the most value from
  Orchestrator.inc, so we can prioritize improvements and reach out for feedback
- The GitHub username of the account signed in to Orchestrator.inc's GitHub integration, sent
  on session-start events so we can see which developers are most active and reach
  out for feedback. It is part of product telemetry with no separate control. See
  "Sharing your GitHub handle" below for exactly what is sent and when
- Reliability data, such as an error type and context, a crash message and
  stack trace after path redaction, an HTTP status, or an agent waiting for
  input
- Basic environment information, such as the Orchestrator.inc version, operating system,
  release channel, and which supported agent types are available
- If you are signed in to AO Cloud: your AO Cloud user ID on every
  desktop and daemon event, and a PostHog person profile holding your email
  address, AO Cloud user ID, and GitHub username. The email is set at sign-in
  and subsequent signed-in launches from the AO Cloud account itself. It is
  never read from git config and is not attached to individual events. See
  "Signing in to AO Cloud" below
- Your GitHub username, when Orchestrator.inc can resolve the account signed in
  to its GitHub integration, on every desktop and daemon event (previously only
  session-start events)
- Desktop onboarding progress, such as viewing, completing, or leaving the
  welcome and GitHub sign-in steps, and a one-time install event on first run
- The Orchestrator.inc version on every daemon event
- A random `install_id` on every desktop and daemon event, the phone's own
  stored `install_id` when available, and a `paired_install_id` on phone events
  while its desktop identity is known,
  and one-way hashes of project or session identifiers when an event needs them
- Coarse mobile-app usage, such as pairing, reconnecting, completing onboarding,
  opening a notification, or using a core action
- Coarse geographic location (country, and where available region and city),
  derived by PostHog from the connection's IP address when it receives each
  event. Orchestrator.inc does not resolve or send precise coordinates, and does not store
  your IP address itself. PostHog may associate this geography with other
  events from the same installation, including a session-start event that
  carries a GitHub handle. Orchestrator.inc uses the geography for aggregate analysis, and
  there is no separate opt-out. Turning telemetry off (see below) stops it
  along with everything else

Orchestrator.inc uses [PostHog](https://posthog.com/privacy) to process remote product
telemetry. PostHog receives standard connection and device metadata, including
the connection's IP address, device type, and operating system, and Orchestrator.inc leaves
PostHog's IP-based location derivation enabled so this coarse geography is
available for aggregate analysis.

The installation identifier lets PostHog group activity from one Orchestrator.inc
installation over time. Hashed project and session identifiers can likewise
group events for the same project or session without sending those identifiers
in plain text. These identifiers are distinct from the explicit GitHub account
attribution on session-start events, but events that share an installation
identifier can still be associated with that attribution in PostHog.

## Usage counts by project

The daemon sends three usage summaries when remote product telemetry is enabled:

| Event | What is counted |
| --- | --- |
| `ao.usage.messages_sent` | New, direct human Chat messages accepted into a session. One count per accepted message. |
| `ao.usage.worker_pr_associated` | A provider PR first associated with an AO worker session. This is not proof the worker created the PR. |
| `ao.usage.worker_pr_merged` | An associated worker PR first observed as merged. |

The project grouping value, `project_telemetry_id`, is a random UUID persisted
locally for each project and installation. It is not a hash of a repository,
project name, path, or content. A different installation gets a different value
for the same repository. The daemon does not send the local project ID, PR URL,
PR title, repository, branch, or message content in these events. Message
summaries also carry a `session_telemetry_id` derived from the random project
value and the local session ID. It groups batches from one session on this
installation without making the local session ID available remotely. The
usual `install_id` and signed-in account attribution still apply.

The message count covers project Chat messages that have a human origin and no
sending worker. Standalone sessions have no project grouping and are excluded.
It includes a newly queued direct message but excludes opening prompts delivered
during session creation, repeated client requests, retries or replays of an
existing prompt, reviewer Chat, worker
automation sent into another session, provider messages, tool calls, and
terminal input in TUI sessions. Counts are stored per session without content.
They flush about every 30 minutes, when a session is archived, when the
desktop app exits, and when the daemon stops. The first flush runs in the
background after daemon startup. Pending counts survive a daemon restart.
A summary is reserved before entering the existing best-effort remote queue.
Usage events wait for PostHog queue capacity rather than being dropped when
the buffer is full. A crash, caller cancellation, or export failure after
reservation can still lose a batch; it will not duplicate the count on restart.
An identity persistence failure pauses draining without discarding pending
counts. Only an actual user opt-out discards them.
If remote events are configured but the PostHog sink cannot start, the daemon
keeps new usage counts locally and leaves them pending until a later start with
a working sink.
The remote sink has a 100,000 event per name per daemon process per rolling
24-hour window emergency cap for these summaries. Events beyond that cap are
dropped after reservation, so their counts cannot be recovered from the local
summary.

PR counts use the provider PR associated with a worker in AO's durable PR
table, not a session spawn or provider poll. One local provider identity is
counted once even if ownership moves between workers. Orchestrator-owned PRs
are excluded. Repeated observations do not add counts. A PR already associated
with a worker when this measurement is first installed contributes one
association count, and an already merged PR also contributes one merged
count. Both carry `historical: true`; newly observed associations and merges
carry `historical: false`. Counts are grouped per project at each full flush,
about every 30 minutes and at daemon or desktop shutdown. The one-minute
archive pass skips the PR scan.
PRs and messages accepted while product telemetry is off are not exported
after a later opt-in.
If events export is disabled in configuration when this version first runs,
backfilled worker PR associations that have not been observed again remain
eligible for historical export if events export is enabled later. An observation
while export is disabled marks that association reported locally. Explicit
product telemetry opt-out discards pending associations before export.

## Automation usage and creation

When desktop product telemetry is enabled, Orchestrator.inc records visits to the
Automations page with `ao.v2.renderer.route_viewed` and `surface: automations`.
It also records these events when you create an automation:

| Event | When it is sent | Automation-specific properties |
| --- | --- | --- |
| `ao.renderer.automation_create_opened` | You open the create dialog | None |
| `ao.renderer.automation_create_succeeded` | The create request succeeds | `project_id_hash`, `schedule_preset`, `prompt_length_bucket` |
| `ao.renderer.automation_create_failed` | The create request fails | The same properties as the success event |

These events also include the standard installation and environment information
described above. The project identifier is hashed before transmission.
`schedule_preset` is limited to `daily`, `weekly`, or `custom`. The
`prompt_length_bucket` describes the UTF-8 byte size of the prompt after trimming
leading and trailing whitespace:

| Bucket | Prompt size |
| --- | --- |
| `xs` | Up to 80 bytes |
| `s` | 81 to 240 bytes |
| `m` | 241 to 800 bytes |
| `l` | 801 to 2,000 bytes |
| `xl` | More than 2,000 bytes |

## Artifact creation

When desktop product telemetry is enabled, Orchestrator.inc records visits to the
Artifacts page with `ao.v2.renderer.route_viewed` and `surface: artifacts`. It
also records these events when you start an artifact session from that page:

| Event | When it is sent | Artifact-specific properties |
| --- | --- | --- |
| `ao.renderer.artifact_create_succeeded` | The session starts | `scope` |
| `ao.renderer.artifact_create_failed` | Starting the session fails | `scope` |

`scope` is `standalone` for the Scratchpad and `project` otherwise. No project
identifier, file name, or artifact content is sent.

The automation events do not include the prompt text, automation name, raw
project identifier, exact schedule, timezone, selected agent, or error message.
Editing an existing automation does not emit these creation events. Closing the
create dialog or failing local validation does not emit a separate event.
Orchestrator.inc uses the outcome events to understand which schedule presets
people choose and whether creation succeeds.

The desktop product-telemetry controls described under "Turn desktop and daemon
telemetry off" also control these events.

## What Orchestrator.inc does not intentionally send

Product telemetry is designed not to include:

- Source code, diffs, commits, or file contents
- Prompts, agent conversations, agent output, or terminal contents
- Shell command arguments, command history, or environment variables
- Repository names, project names, branch names, or plain-text file paths
- API keys, access tokens, passwords, or other credentials
- Names, or email addresses other than the one on your signed-in AO Cloud
  account (invitee emails are never sent)

The explicit account fields are the GitHub owner segment, your authenticated
GitHub username, and, only if you sign in to AO Cloud, your AO Cloud user ID and
email address. Both are described under "What Orchestrator.inc sends". The
owner segment is limited to the owning organization or account and never
includes the repository, path, or URL. Orchestrator.inc does not intentionally send other
account identity fields, but PostHog can associate related events when they
share an installation identifier with a session-start event that carries your
GitHub handle.

The optional website waitlist is separate from product telemetry. If you submit
an email address, company role, and social profile there, they are used to manage
that waitlist as described in the [privacy policy](https://orchestrator.inc/privacy).

## How Orchestrator.inc limits the data

- Orchestrator.inc generates a random installation identifier on first run. It is stored at
  `~/.ao/data/telemetry_install_id` (or under `AO_DATA_DIR`) and does not encode
  a personal account. Desktop and daemon events include it as `install_id`, even
  when the PostHog `distinct_id` changes at sign-in, sign-out, or reset. It
  persists across those transitions and process restarts. A paired phone uses
  its own `distinct_id` and installation identifier, and includes the desktop
  identifier as `paired_install_id`
  only after its authenticated identity request succeeds. Pairing changes clear
  the old value until the new desktop responds. These properties allow events
  from the same installation to be associated across accounts and with the
  GitHub handle sent on a session-start event.
  On upgrade from a build that used the desktop account as the phone's PostHog
  identity, the phone resets that SDK identity once before sending new events.
  Its stored phone installation identifier is retained.
  While signed in, the daemon reports under the AO Cloud user ID. On sign-out
  or account switch, its anonymous `distinct_id` changes to avoid attributing
  new anonymous events to the previous person.
- Project and session identifiers included in telemetry are one-way hashed.
  Hashing hides the plain text but still allows related events to be grouped.
- Absolute local paths and local application URLs detected in desktop events
  are replaced with redaction markers before the events are sent.
- Daemon events sent to PostHog and mobile events accept a fixed set of
  properties; unexpected fields are discarded.
- Event rates are limited to reduce repeated background activity and error
  loops.
- Remote HTTP 5xx events are not sampled. The minute rollup and the daily
  rate limit bound them, and real HTTP 500 responses also go to the existing
  gated Sentry capture once. The local diagnostic stream keeps every
  occurrence. CLI usage errors are one event per command path and error kind
  per install per completed UTC day, with `command_path`, `error_kind`, and
  `count`. At most 20 of those events are sent per day. The minute rate limit
  does not apply to this event, because each one is already a summary. Paths
  that are not a known `ao` command are reported as `other`. No raw arguments
  are included. Each event is stamped at the last millisecond of that UTC day.
  The on-disk summary is removed on opt-out and when the install ID is retired.
- Renderer API errors are not sampled. A daemon-unavailable report is skipped
  while the daemon is still starting. HTTP 409 responses are dropped only for
  `POST /api/v1/sessions/:id/workspace/diffs`,
  `GET /api/v1/sessions/:id/conversation/config-options`,
  `GET /api/v1/sessions/:id/conversation/models`, and
  `GET /api/v1/sessions/:id/conversation/skills`. Codex
  account management being unwired, and an already-closed shell terminal, are
  not analytics events. An expired GitHub token still reports. A Codex login
  terminal failure is `CODEX_LOGIN_TERMINAL_UNAVAILABLE`. An incomplete Codex
  account setup is `CODEX_ACCOUNT_SETUP_INCOMPLETE`. Both still report on the
  account routes. The account event stream stays open for setup-incomplete
  and for account management that is not wired in this process, so those
  conditions do not reconnect in a loop. A failed project add emits
  `ao.renderer.project_add_failed` with `error_code`. A failed project clone
  emits `ao.renderer.project_clone_failed` with `error_code`. The code is the
  daemon's API error code, or `DAEMON_NOT_READY`, `REQUEST_FAILED` or
  `NO_PROJECT` when the request never reached a usable response. A clone the
  user cancels emits nothing.
- An updater failure streak emits one `ao.renderer.update_failed` until a
  successful download of that target, or a successful check for a check-phase
  streak, clears it. Retries in the streak do not emit again. Each install
  also emits one `ao.renderer.update_failures_daily` event, with the UTC day
  and failure count, after that day ends. A rejected capture leaves the streak
  unsent. A restart keeps the open streak.
- `ao.review.triggered` counts a newly created or restarted run and does not
  include a `reused` property. Reused sweeps are not events, and
  `trigger_failed` is no longer a subset of `triggered`. The series breaks on
  the merge of this change. Issue #4665 previously counted every attempt.
- Session recording is disabled in the desktop and mobile apps. Orchestrator.inc does not
  automatically record screens, clicks, or touches.
- Person profiles are off for anonymous events. A profile is created when a
  GitHub username is first resolved (one update that sets the username and
  clears build details older versions stored on the profile) and when you sign
  in to AO Cloud (your email, AO Cloud user ID, and GitHub username). After an
  AO Cloud sign-in, desktop and daemon events are linked to that profile. A
  paired phone retains its own identity and reports the Cloud ID as an event
  property. Project-add events still carry the GitHub owner segment
  described above, so this is not an anonymity guarantee.

Separately from remote telemetry, the daemon can keep a local copy of
operational events in Orchestrator.inc's SQLite database. While local telemetry is active, Orchestrator.inc
periodically prunes records older than 30 days by default. Set
`AO_TELEMETRY_RETENTION` on the daemon to a Go duration such as `168h`, or a
whole number of days such as `7`, to change this window (minimum 24 hours).
Expired records are deleted in 5,000-row batches with pauses between batches.
After each hourly sweep, incremental vacuum reclaims free database pages in
256-page batches with pauses, stopping when fewer than 256 remain or SQLite
cannot make further progress. This data stays under `~/.ao` on your
machine; this setting does not change remote telemetry retention.

The upgrade that enables incremental vacuum performs a one-time full database
rewrite. Large databases can take longer to upgrade and require temporary free
disk space for the rewrite.

## Signing in to AO Cloud

When you sign in to AO Cloud, the desktop app calls PostHog `identify` with your
AO Cloud user ID as the identity and sets three person properties:
`email`, `ao_cloud_user_id`, and `github_login`. The initial anonymous
`distinct_id` is merged into that identity on first sign-in. Later signed-in
daemon events use the AO Cloud user ID directly. A paired phone keeps its own
identity and carries the Cloud user ID only as an event property. Account fields
on events are the user ID and, once resolved, the
GitHub username (as `ao_cloud_user_id` and `github_actor`). The installation
properties described above are also event fields. The email is a person
property only. It comes from the AO Cloud
account you signed in with and is never read from git config. The daemon and the
mobile app never receive your email; they receive only the user ID. Signing out
stops attributing new desktop events to you. Only the production WorkOS sign-in
is identified.

AO Cloud itself reports a few server-side events for signed-in users, currently
`invite.sent` (organization role and a hash of the organization ID, never the
invitee's email). Event names for pricing, checkout, and subscriptions are
reserved with fixed property allowlists but nothing sends them yet. These
server-side events are off unless the AO Cloud operator configures PostHog and
are not controlled by the desktop opt-out below, which cannot reach the hosted
service. Contact us to have an account's data removed.

## Sharing your GitHub handle

Orchestrator.inc resolves the GitHub account signed in to its GitHub integration (at
daemon start, and again in the desktop app shortly after launch) and includes
that username on desktop and daemon events as `github_actor`, and on the PostHog
person as `github_actor` and `github_login`. It is sent both as the event property
`github_actor` and as a PostHog person property on Orchestrator.inc's shared installation
person, which lets us group product activity by GitHub username and reach out to
active users for feedback. Other events from the same installation can be
associated with that person in PostHog.

Orchestrator.inc only sends the handle when the signed-in account is a personal (human)
account; it never sends an organization or a bot token, and if no GitHub token is
available it sends nothing. The handle is part of product telemetry and has no
separate switch: turning telemetry off (see below) stops it. Anything already
stored in PostHog from earlier events is not deleted retroactively.

## Turn desktop and daemon telemetry off

The simplest way is **Settings > General > Privacy > Share usage analytics**.
Turning it off stops PostHog analytics in the desktop interface, the daemon, and
any paired phone, and resets the PostHog identity in the desktop app so no
identity (user ID, email, GitHub username) stays in the client. It takes effect
immediately and is remembered across restarts (it is a marker file,
`telemetry_opt_out`, next to the installation identifier). A paired phone picks
it up the next time it connects and keeps it until a paired desktop that has
analytics on tells it otherwise. Turning it back on resumes sending; the desktop
interface uses a fresh anonymous identifier until the next restart. Data already
sent to PostHog is not deleted.

The **Share error events** control on the same page covers error diagnostics
only: the daemon's Sentry capture of reportable 5xx errors, failed agent
relaunches answered as 409 `AGENT_RESUME_FAILED`, and handler or background
goroutine panics; and Electron main capture of process crashes, uncaught
exceptions, unhandled rejections, and reportable renderer errors. Sentry
receives fixed failure or panic labels and allowlisted error codes instead of
raw error messages. It groups HTTP faults by registered route and error code,
while retaining the AO function that recorded the failure as the culprit.
Agent relaunches group by operation and failure step, with a fixed step tag
and no HTTP route tag.
An event may include a bounded `git`, `db`, `agent`, or `network` area when the
error provides evidence. It keeps stack function and line information, but
removes stack paths, request data, project, session, repository and branch
names, and panic text. Electron events use fixed titles and scrubbed exception
values with the same installation ID as Sentry `user.id`.
Canceled requests, transient 503 responses, and expected upstream link-preview
502 responses do not create Sentry issues. Registered route templates remain
available for diagnosis. Unmatched URLs are reported as `unmatched` instead of
their raw path. Every daemon Sentry event has release and environment values.
The daemon tags these error events with the same `install_id` and sets Sentry
`user.id` to that identifier when available. If it cannot be read, consented
error reporting continues without either identity field. These fields do not
include email or local paths. The environment-variable controls below also
govern the desktop/daemon product-telemetry paths, for example in managed
environments. Set all three variables in the environment used to launch
Orchestrator.inc:

```bash
export AO_TELEMETRY_RENDERER=off
export AO_TELEMETRY_EVENTS=off
export AO_TELEMETRY_REMOTE=off
```

Then restart Orchestrator.inc. `AO_TELEMETRY_RENDERER=off` disables events sent directly by
the desktop interface. `AO_TELEMETRY_EVENTS=off` disables daemon event capture,
including its local copy. `AO_TELEMETRY_REMOTE=off` explicitly disables daemon
export to PostHog.

The values must reach the desktop app process itself. For example, variables in
a shell startup file may not be inherited when you launch Orchestrator.inc from the macOS
Finder or Dock.

If you run the daemon without the desktop app, event capture and remote export
are already off unless you enable them.

These environment variables do not control the mobile app; the in-app switch
above does, through a paired desktop. A phone that was never paired to a desktop
has no in-app opt-out. Turning
desktop or daemon product telemetry off does not
delete events already sent to PostHog, remove the local installation identifier,
or delete existing local product-telemetry records. Automatic deletion of those
local records older than the configured retention window (30 days by default)
resumes if daemon event capture is enabled again.

## Questions or corrections

For the broader data policy, retention information, and contact options, see
the [Orchestrator.inc privacy policy](https://orchestrator.inc/privacy). You can report a problem
with this documentation in the
[GitHub repository](https://github.com/OrchestratorInc/agent-orchestrator).
