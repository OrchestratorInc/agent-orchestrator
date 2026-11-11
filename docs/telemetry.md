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

Share usage analytics and Share error events are on by default in production
desktop releases for fresh installs and existing users, unless a user turned
either choice off or an environment setting disables it. Both can be turned
off at any time in Settings > General > Privacy. A packaged desktop release
also enables telemetry for the daemon it starts.
Development builds and daemons started directly do not send remote telemetry by
default.

## Sentry projects

The packaged desktop app gives its spawned Go daemon the daemon Sentry project
DSN through `AO_SENTRY_DSN`. An explicit `AO_SENTRY_DSN` value takes precedence;
without one, development and self-built copies hand the daemon an empty value
(see Published builds only below). Daemon error reporting remains subject to the
existing Share error events control.

Electron main and the renderer have a separate desktop Sentry project. Their
default DSN is distinct from the daemon project; Electron main uses
`AO_ELECTRON_SENTRY_DSN` for an explicit override. Without one, development
and self-built copies use an empty Electron DSN. The renderer uses this
main-owned sender and has no independent DSN override. Published stable and
nightly builds send error events through Electron main only when Share error
events is enabled, subject to the install sampling and daily caps below. This
covers fatal crashes, handled main-process failures, unhandled renderer window
errors, and rejected promises.

On a fresh or existing packaged install, Share error events starts on unless
it was explicitly turned off. You can turn it off at any time in Settings.
Renderer diagnostics pass through the existing main-owned consent boundary;
the renderer has no direct Sentry transport. The mobile app keeps its own
Sentry project and DSN. Earlier error-sharing choices remain effective on
upgrade. On Windows, the policy is saved with a flushed temporary file and
an atomic replacement that retries temporary file locks, so the same Settings
choice controls Electron and daemon Sentry there. Share usage analytics is a
separate choice for PostHog.
Once enabled, renderer exceptions go to Electron Sentry rather than PostHog. Failed
Sentry sends can wait in an offline queue under AO's Electron data directory.
Error events can include a standard exception type, bounded function names,
bounded line and column numbers, and a validated app relative JavaScript asset
filename. Anonymous minified frames (`at app://renderer/assets/x.js:L:C`) are
kept, and a frame whose function name is not on the fixed allowlist is kept as
`renderer_frame` with its location only, so a renderer title reads, for
example, `Renderer TypeError in assets/useSessionWorkspaceFiles:123:45`. Release
builds stamp the Electron main and renderer bundles with Sentry debug IDs and
upload their source maps to the ao-electron project; the maps are not shipped
in the app. An event may carry the debug ID of each app bundle in its stack and
the bundle's app URL (`app://renderer/assets/...` or `app:///.vite/build/...`),
never an install path. Local
filesystem paths, full URLs, and raw exception messages are removed before sending.
The app's global renderer error boundary binds handlers independently of
PostHog setup. Electron main still checks Share error events before sending.
Preload installs main-world error and rejection handlers before renderer imports
and reads the consent policy itself. An import failure can therefore report even
when the renderer never starts. Early reports contain fixed text, a standard
exception type, and validated asset locations only. Once the renderer starts,
the same listeners hand off to its normal reporter. Early captures are not
replayed through the later renderer boundaries.
Router error boundaries and query/mutation cache callbacks also report unexpected
client failures. Query keys, mutation inputs, route parameters and error text are
not sent. API failures retain a local ownership marker through message wrapping,
so these callbacks do not duplicate the API client's report or escalate expected
API state. Repeated observations of the same exception object are reported once.
Background side-chat restore and snapshot failures use the same main-owned
consent gate, with fixed titles and no launch-state payload. Expected connection,
authentication, cancellation and stale-attachment states stay out. Repeated
recovery reports are limited to five per minute and 200 per UTC day.
Turning off Share error events drains the sender and removes that queue before
the opt-out is complete. If the app exits during that cleanup, startup purges
the leftover queue while sharing is off, before a later opt-in can start the
sender.

The mobile app uses the existing ao-mobile Sentry project in non-development
builds when mobile telemetry is active. Its JavaScript error sender starts after the
saved phone analytics preference is restored. It follows the paired desktop's
analytics opt-out, which remains sticky while the phone is unpaired or waiting
for a replacement desktop. Development builds and
`EXPO_PUBLIC_AO_TELEMETRY_DISABLED=1` do not initialize it. An explicit empty
`EXPO_PUBLIC_SENTRY_DSN` disables it; otherwise the app uses its bundled
mobile DSN. Reportable React render failures, uncaught JavaScript failures,
and server API failures are captured. Native crash capture is disabled because
the native SDK's offline envelope queue cannot be cleared at the phone's
analytics opt-out boundary. No failed mobile Sentry send is persisted for retry.
The sender checks consent both before capture and immediately before network
send, so an event still queued when opt-out arrives is discarded.

Mobile Sentry receives fixed error titles and categories, safe stack line
numbers with generic frame names, release version and build number, and the
production or preview environment. It receives no raw error message, URL,
request body, repository or workspace path, project or session identifier,
credential, or account identity. Its `user.id` is the authenticated paired
desktop installation ID only. It is absent before pairing, during repair, and
after unpairing or a failed identity refresh. The phone retains its separate
PostHog identity across those changes. A paired Sentry error can therefore be
associated with the desktop installation. The Sentry package and Expo plugin
require a native prebuild before a mobile binary can include this change.
All EAS profiles disable automatic Sentry source map and debug file upload,
so builds do not require Sentry credentials. Mobile Sentry stack traces use
generic frame names and line numbers; source maps are not available for them.

For workspace file streams, a deleted worktree or a linked worktree whose
owning Git repository disappeared returns a missing-workspace or
`PROJECT_FOLDER_MISSING` 404. A recognized Git lock returns retryable
`WORKSPACE_GIT_BUSY` 503. Other Git read failures remain
`WORKSPACE_GIT_READ_FAILED` 500 for investigation; the app does not infer
session death or remove worktrees from these read failures.

### Published builds only

Sentry initializes only in a published build: the version must be a
release-stamped stable (`0.14.10`) or nightly (`0.14.11-nightly.202611100625`)
version, and the build must carry the `AO_PUBLISHED_BUILD=1` marker that only
the release workflow (`build-artifacts.yml`) bakes into Electron main. A
development run, a fork or self-built copy (for example `0.13.0`,
`0.14.10+fork.1`, `0.13.0-company.7`), and an edge or PR build send nothing to
either Sentry project. The daemon applies the same version pattern to the DSN
it receives.

### Sampling and daily caps

Sentry quota is shared by every install, so desktop and daemon errors are
sampled by install rather than by event:

- An install is in the error sample when `sha256(install_id)`, read as a
  big-endian 32-bit integer, modulo 10 is 0. Electron main and the daemon use
  the same rule, so about one install in ten sends non-fatal errors from all
  of its processes, and the rest send none. An install whose identifier cannot
  be read is outside the sample.
- Fatal events send from every install: Electron main uncaught exceptions,
  Electron process crashes (`process_exit`), and daemon panics. Renderer errors,
  including unhandled window errors and rejections, are non-fatal and sampled.
- Every event carries a `sample_rate` tag: `0.1` for sampled non-fatal events
  and `1` for always-sent fatal events. Multiply counts of `sample_rate:0.1`
  events by 10 to estimate the real volume.
- Electron main caps every desktop event (main and renderer) after scrubbing,
  keyed on its final Sentry fingerprint: at most 3 events per fingerprint and 25
  events in total per install per UTC day. The counters are saved in
  `telemetry_sentry_budget.json` in the AO data directory, so a renderer reload,
  a new window or an app restart does not reset them. The renderer has no
  exception budget of its own.
- The daemon caps non-fatal errors at 25 per install per UTC day across all
  fingerprints, on top of its per-fingerprint budget below. This counter is in
  memory and resets when the daemon restarts.

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
- Slow database writes: when one write holds the daemon's database write lock
  for a second or more, `ao.daemon.write_lock_held` sends how long it was held
  (`duration_ms`) and the names and line numbers of the four innermost code
  functions that held it (`caller`). It never includes file paths, database
  contents, or message text
- Basic environment information, such as the Orchestrator.inc version, operating system,
  release channel, and which supported agent types are available. Desktop and
  daemon events carry the operating system name (`$os`) and version
  (`$os_version`), `platform`, CPU architecture (`arch`), and the update
  channel you chose (`release_channel`)
- How Orchestrator.inc was installed, on every desktop and daemon event:
  `install_source` is one of `npm-bootstrap` (`ao start`), `dmg`, `brew-cask`,
  `exe`, `appimage`, `deb`, `rpm`, `dev`, `unknown`, or `other`, and
  `installed_at` is the UTC day the app first ran. Both come from
  `~/.ao/app-state.json`; no download path or file name is sent
- Whether the app is running in CI (`is_ci`) or a container (`is_container`),
  detected independently from well-known CI environment variables and
  container marker files. A development container has `is_ci: false` and
  `is_container: true`; CI inside a container has both true. Only the booleans
  are sent, never the variables themselves
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
- One-time lifecycle markers: `ao.app.installed` is sent once, on the first
  daemon start of a fresh installation, and says whether the desktop app or the
  CLI started the daemon (`launcher`). An installation whose local database
  already has a project or a session (an upgrade that created the
  installation identifier file) spends the marker without sending it.
  `ao.install.created` is no longer sent. `ao.app.first_opened` is the single
  first-open event: the desktop sends it on the installation day, before any
  session exists, at most once per installation.
  `ao.sessions.spawned` and `ao.projects.created` carry `first_for_install`,
  true only for the installation's first session or project. These are
  recorded by marker files under `~/.ao/data` (`telemetry_installed_emitted`,
  `telemetry_first_open_emitted`, `telemetry_first_session_spawned`,
  `telemetry_first_project_added`). Installations that sent
  `ao.install.created` on an earlier build already hold
  `telemetry_first_open_emitted`, so they never send `ao.app.first_opened`
  after an upgrade. The `ao.sessions.first_spawned` and
  `ao.projects.first_added` events (formerly `ao.onboarding.first_*`) are
  deprecated: they fire again whenever the session or project list is empty,
  and are kept only so existing dashboards continue
- A fixed experiment bucket, `experiment_cohort` (a whole number from 0 to
  99), on every desktop and daemon event. It is picked at random on the first
  run that reaches it and stored in `~/.ao/data/telemetry_experiment_cohort`,
  so onboarding experiments can compare bucket ranges without feature flags.
  It is random and says nothing about you
- Whether sessions get used and deliver: who started a session and how it
  ended, plus categories and timings on the usage counts below. These carry
  only categories, counts, and durations, never message text, titles, branch
  names, or URLs. See "Usage counts by project" and "How sessions start and
  end" below
- Desktop onboarding milestones separately record an app open on the install
  day and the first eligible user CLI invocation. Returning on a later day is
  computed from PostHog retention, not sent as an event.
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

Daemon `project_id_hash` uses the random install ID as a salt before hashing
the local project ID. This keeps the session spawn grouping stable within one
installation while preventing a short, guessable project slug from yielding
the same hash across installations. Usage summary events retain their separate
random `project_telemetry_id`.

Desktop and daemon events carry `days_since_install` when the persisted install
ID file has a valid timestamp. The daemon reads it at export time; the desktop
receives that timestamp during telemetry bootstrap and recalculates the age
for each event. The daemon also reads `lifetime_sessions` from
a SQLite counter that increases when a session is committed and survives its
later deletion. The daemon snapshots the count when an event enters its remote
sink, before asynchronous export. The desktop reads that counter from the
loopback daemon during bootstrap, and a paired phone receives it through the
authenticated telemetry identity route. If the count cannot be read, it is
omitted. Historical sessions
deleted before this counter was introduced cannot be reconstructed.
`is_signed_in` is on every desktop and daemon event. It is true while the
emitting surface knows an AO Cloud sign-in and false otherwise. The daemon
keeps the sign-in it last recorded across restarts (the
`telemetry_bound_user` file). Signing out on the desktop removes that file and
rotates the anonymous identifier before the sign-out completes, so the sign-out
holds even when the daemon was not running to hear it and the app quits right
after. A launch that finds that file without a stored sign-in retires it the
same way. A paired phone omits the value until its
desktop answers. The desktop reads `lifetime_sessions` at launch, again once
the daemon is ready if it was not available then, and again after the desktop
creates a session; a session started from the CLI is reflected on the desktop's
next refresh.
`lifetime_sessions` counts sessions the daemon accepted, including an
asynchronous start that later failed. These properties contain no local
project or session ID.

Every desktop and daemon event also carries `install_id`, `days_since_install`,
`lifetime_sessions` and `is_signed_in` from the same property builders, and
the CLI's events are exported by the daemon with the same properties.

Session spawn events use `created_by` from the durable session attribution
record to describe whether a user, orchestrator, schedule, or automation
started the session. Session telemetry reports `kind: standalone` when the
session has no project. Failed desktop orchestrator spawn outcomes include
the daemon error code and HTTP status when available. These values contain
no error message, project name, or request ID.

The onboarding milestones use durable local reservations. A first app open is
recorded only on the install day, before any session exists, and only once
per installation (an exclusive marker in the data directory). The marker is
taken only when the event is about to be sent, never while the session count
is still unknown, and not while analytics are off or the event is on the kill
switch.
The first eligible user CLI invocation reserves a local marker before export. None of
these milestones treats an idle session as a successful session.
The first project and first session are identified by `first_for_install` on
the committed project and session events; the first-session claim is taken only
by a session that became usable. The renamed `ao.sessions.first_spawned` and
`ao.projects.first_added` events remain deprecated. `ao.sessions.first_spawned`
is sent only for a start that succeeded on its first attempt, so when an
installation's first start failed and a retry succeeded, `ao.sessions.spawned`
carries `first_for_install: true` without a matching `ao.sessions.first_spawned`.
`lifetime_sessions: 2` on `ao.sessions.spawned` identifies the second accepted
session; an earlier start that failed also counts.

| Event | Authoritative source | Deduplication |
| --- | --- | --- |
| `ao.app.first_opened` | Desktop opens on the installation day, before any session | Exclusive marker `telemetry_first_open_emitted` |
| `ao.cli.first_used` | First eligible user CLI invocation, before command execution; the command may later fail | Install marker file |
| `ao.projects.created` with `first_for_install` | First committed project registration | Durable project registry |
| `ao.sessions.spawned` with `first_for_install` | First session that became usable | Durable install milestone |
| `ao.sessions.spawned` with `lifetime_sessions: 2` | Second accepted session | Durable lifetime counter |
| `ao.agents.missing` | Agent readiness reports zero installed agents | Once per desktop launch |
| `ao.agents.install_clicked` | User starts an agent install from Settings | One event per accepted click |

`ao.setup.checked` reports the desktop's startup system check: `git`, `tmux`,
`gh` and, once the app has already asked GitHub, `gh_signed_in` (booleans),
`agents_installed` and `agents_signed_in` (counts), and
`agents_installed_ids` and `agents_signed_in_ids` (sorted, comma-joined agent
IDs from AO's own registry). It reuses the checks the app already ran at
startup, is sent the first time they answer, and again whenever the result
changes. No paths, version strings or usernames are included.

The per-launch agent inventory and the AO Cloud offering summary are written to
the desktop app's log instead of PostHog.

## Usage counts by project

The daemon sends four usage summaries when remote product telemetry is enabled:

| Event | What is counted |
| --- | --- |
| `ao.usage.messages_sent` | New, direct human Chat messages accepted into a session. One count per accepted message. |
| `ao.usage.worker_pr_associated` | A provider PR first associated with an AO worker session. This is not proof the worker created the PR. |
| `ao.usage.worker_pr_merged` | An associated worker PR first observed as merged. |
| `ao.prs.closed` | An associated worker PR first observed as closed without merging. |

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
carry `historical: false`. Historical counts are grouped per project; each new
outcome is sent as its own event with `count: 1`, so summing `count` gives the
same totals either way. PR summaries go out at each full flush, about every 30
minutes and at daemon or desktop shutdown. The one-minute archive pass skips
the PR scan. A PR already closed when closed outcomes started being measured
is not reported as closed.
PRs and messages accepted while product telemetry is off are not exported
after a later opt-in.
If events export is disabled in configuration when this version first runs,
backfilled worker PR associations that have not been observed again remain
eligible for historical export if events export is enabled later. An observation
while export is disabled marks that association reported locally. Explicit
product telemetry opt-out discards pending associations before export.
### Detail on the usage counts

`ao.usage.messages_sent` keeps `count` as defined above and adds:

- `steer_count`: guidance you steered into a running turn.
- `send_count`: messages you sent with `/send`, meaning `ao send` outside an
  AO session or a TUI session's composer. Messages an AO session sends another
  session are not counted.
- `desktop_count`, `mobile_count`, `cli_count`: all of the above split by the
  client the message came from. The daemon works the client out from how the
  request arrived; `cli` covers any local client without a browser origin.
- `kind` (`orchestrator` or `worker`) and `harness` of the included project
  session. Standalone message counts are excluded as noted above.

New (`historical: false`) PR outcomes add:

- `kind` and `harness` of the owning worker when known, plus
  `scm_provider` (`github` or `gitlab`).
- `ms_since_spawn`: from the worker starting to the PR opening, merging, or
  closing.
- `ms_since_open` (merged and closed): from the PR opening to its outcome.
- `review_rounds` (merged and closed): reviews recorded on the PR.
  `ao_review_used`: whether an AO review ran on it.
- `merged_via` (merged): `desktop`, `mobile`, or `cli` when merged through
  Orchestrator.inc, and `github` when the merge happened on GitHub. It is
  omitted when the external merge surface is unknown, such as GitLab.
- `first_for_install` (associated and merged): true only for the
  installation's first new worker PR and first merge, recorded by
  `telemetry_first_pr_opened` and `telemetry_first_pr_merged` under
  `~/.ao/data`. An installation with earlier, already measured PRs reports
  false.

## How sessions start and end

| Event | When it is sent | Properties |
| --- | --- | --- |
| `ao.sessions.spawned` | A session becomes usable: a synchronous start returns, or an asynchronous Chat start finishes its worktree, Setup, controller start and opening prompt | `kind`, `harness`, `spawn_duration_ms`, `duration_ms`, `created_by`, `mode`, `async`, `model`, the step timings below and, for sessions a request started, `surface` |
| `ao.sessions.spawn_failed` | A start attempt fails, including an asynchronous Chat start that fails in the background | `kind`, `harness`, `spawn_duration_ms`, `duration_ms`, `error_kind`, `error_code`, `failed_step`, `cause`, `created_by`, `surface`, `mode`, `async`, `model` and the step timings below |
| `ao.sessions.spawn_cancelled` | A start attempt is cancelled on purpose (`SPAWN_CANCELLED`: the client left, the session was killed while starting, or the daemon shut down) | The same properties as `ao.sessions.spawn_failed` |
| `ao.sessions.ended` | A session is killed, replaced, torn down after its pull requests merged or its issue closed, or its runtime dies | `kind` (`standalone`, `worker`, or `orchestrator`), `harness`, `outcome`, `duration_ms`, `had_pr`, `waiting_input_count`, `created_by`, `session_telemetry_id`, and for project sessions `project_telemetry_id`. Project sessions use the same grouping IDs as the usage counts; a standalone session's `session_telemetry_id` is a one-way hash of its session ID salted with the installation identifier |

- `spawn_duration_ms` is the whole launch: from the session service accepting
  the spawn request (desktop, mobile, CLI, API, an orchestrator, a schedule or
  issue intake all pass through it) to the session being usable, measured with
  the daemon's monotonic clock. It does not include the agent's first reply.
  `duration_ms` keeps its earlier meaning, the time until the spawn request
  was answered, which for an asynchronous Chat start is before the session is
  usable. A retry of a failed start is its own attempt, timed from the retry,
  without `duration_ms`. Each attempt reports one outcome. A start cancelled on
  purpose (the session is killed while starting, or the daemon shuts down)
  reports `ao.sessions.spawn_cancelled`, so it does not count as a launch
  failure; a start that runs out of its time budget reports
  `ao.sessions.spawn_failed` with `error_code: SPAWN_TIMEOUT` and
  `cause: timeout`, whatever error the step that was running returned (a git
  or agent process killed by the deadline reports "signal: killed" or "peer
  disconnected"). A start interrupted by a daemon restart reports no event.
- Step timings, in milliseconds, are sent only for the steps that ran:
  `model_check_ms` (validating the model and effort against the saved
  catalog), `branch_fetch_ms` (refreshing the default branch),
  `worktree_ms` (creating the git worktree), `setup_ms` (the project's Setup
  steps) and `agent_start_ms` (starting the terminal agent or the Chat
  controller).
- `failed_step` is `model_check`, `branch_fetch`, `worktree`, `setup`,
  `agent_start`, `prompt` or `other`.
- `cause` is a fixed value, never error text: `timeout`; for git and worktree
  failures `branch_exists`, `path_exists`, `locked`, `permission`,
  `path_too_long`, `not_a_repo`, `killed` or `other`, classified from git's
  stderr inside the daemon; for agent and Chat starts `exited`,
  `no_response`, `not_logged_in`, `killed` or `other`. Sentry spawn errors
  carry `failed_step` and `cause` as tags.
- `mode` is `chat` or `terminal` (absent when the start failed before a mode
  was chosen), `async` is whether the start went on in the background after
  the request was answered, and `model` is a fixed Claude family alias
  (`fable`, `opus`, `sonnet`, `haiku`, optionally suffixed `[1m]`) or `custom`.
  Every concrete, typed, or discovered ID is bucketed as `custom`, even when
  the saved catalog lists it: gateway and custom-provider catalogs may contain
  private organization names. `model` is absent when the harness default was
  used without naming one, or model resolution did not record it. Like every daemon event
  they also carry `$os`, `platform` and `arch`, whichever client launched the
  daemon.
- `surface` is `desktop`, `mobile`, or `cli`, as above.
- `created_by` is `user`, `orchestrator` (an orchestrator ran `ao spawn`),
  `scheduled` (an automation's schedule), or `automation` (issue intake, or a
  worker session starting another session). It is stored in the local
  database next to the session. Sessions created before this was recorded
  report no `created_by`.
- `outcome` is `pr_opened` (the session has a pull request), `completed` (a
  done report or a closed issue without a pull request), `killed` (stopped
  while the agent was working), `abandoned` (stopped while it was idle or
  waiting on you, with nothing delivered), or `failed` (its runtime died).
- `waiting_input_count` is how many times the session stopped to wait for
  you.
- An ended event claims its session once in local storage after the terminal
  state is durable. Concurrent end paths cannot export duplicate outcomes.

## Automation usage and creation

When desktop product telemetry is enabled, Orchestrator.inc records visits to the
Automations page with `ao.routes.viewed` and `surface: automations`.
It also records these events when you create an automation:

| Event | When it is sent | Automation-specific properties |
| --- | --- | --- |
| `ao.automations.create_opened` | You open the create dialog | None |
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

The daemon also emits `ao.automation.created` once after successfully saving a
new definition, whether created through the desktop, CLI, or API. Its only
automation-specific properties are `template_id` and `use_case`, both restricted
to closed vocabularies. It does not include the automation ID, prompt, name,
repository, project ID, or generated output. Existing desktop/daemon telemetry
controls apply; delivery is best effort and a telemetry failure does not fail
creation.

`template_id` is one of `custom`, `daily_pr_review_v1`,
`daily_issue_triage_v1`, or `weekly_release_notes_v1`. It records the chosen
starting point and stays fixed even if the prompt changes. `use_case` is one of
`pr_review`, `issue_triage`, `repo_digest`, `ci_triage`, `release_notes`,
`security_audit`, `dependency_maintenance`, `docs_maintenance`, `other`, or
`unknown`. The desktop derives the category from the selected starter; custom
definitions use `unknown`. CLI/API clients may optionally supply or update a
category, including `other`. Older definitions and clients default to
`custom` / `unknown`.

Template origin is client-declared, not verified. The desktop sets `templateId`
from the selected starter, but CLI/API callers can supply any allowed template
ID with an arbitrary prompt, even when they did not use that starter. Scripted
or agent-driven creates can therefore inflate template counts. Treat
`template_id` as a declared starting point, not proof of a template selection
or prompt contents; it cannot reliably measure desktop starter adoption across
all clients.

For intent analysis, count `ao.automation.created` grouped by these two fields.
Do not add renderer success events to this count: they describe the same desktop
creations and exclude CLI/API creations. Updates, runs, and startup migration do
not emit creation events. This measures declared intent at creation, not current
active usage, successful execution, or the exact contents of a customized prompt.
Existing events cannot be used to reconstruct historical automation intent.

## Artifact creation

When desktop product telemetry is enabled, Orchestrator.inc records visits to the
Artifacts page with `ao.routes.viewed` and `surface: artifacts`. It
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

## Chat import

When desktop product telemetry is enabled, Orchestrator.inc records these events
when you import chats from other agents:

| Event | When it is sent | Import-specific properties |
| --- | --- | --- |
| `ao.session_imports.offer_shown` | An import offer appears | `surface` |
| `ao.session_imports.scan_completed` | A scan for chats ends | `outcome`, `candidate_count`, `duration_ms` |
| `ao.session_imports.completed` | An import ends | `source`, `created_count`, `existing_count`, `failed_count`, `folder_count`, `agents` |
| `ao.session_imports.first_opened` | The first open of an imported chat ends | `agent`, `outcome`, `error_code`, `duration_ms` |

`surface` is `home` or `project_board`. `source` is `home_folders`,
`project_offer`, `project_dialog`, or `settings`. The scan `outcome` is
`succeeded` or `failed`. The first-open `outcome` is `succeeded`, `failed`, or
`open_elsewhere`. `agents` lists the agents of the imported chats, such as
`claude-code,codex`, and `agent` names one; both accept only the ids of agents
AO can import from. `error_code` is the API error code, such as
`CHAT_RESUME_FAILED`.

The offer and scan events are sent at most once per surface each time the app
starts. The import and first-open events are sent only when you import or open
an imported chat.

No chat title, chat content, folder name, path, project identifier, session
identifier, or error message is sent.

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
  Daemon project hashes are salted by the random install ID. Hashing hides
  the plain text while allowing related events on that install to be grouped.
- Absolute local paths and local application URLs detected in desktop events
  are replaced with redaction markers before the events are sent.
- Daemon events sent to PostHog and mobile events accept a fixed set of
  properties; unexpected fields are discarded.
- Event rates are limited to reduce repeated background activity and error
  loops.
- Remote HTTP 5xx events contain only a count and rollup window. The minute
  rollup and daily rate limit bound them. Reportable HTTP errors still go to
  the existing gated daemon Sentry capture once, and local logs and local
  telemetry retain each occurrence with diagnostic fields. CLI usage errors
  are one event per command path and error kind
  (`ao.usage.cli_usage_errors`) per install per completed UTC day, with `command_path`, `error_kind`, and
  `count`. At most 20 of those events are sent per day. The minute rate limit
  does not apply to this event, because each one is already a summary. Paths
  that are not a known `ao` command are reported as `other`. No raw arguments
  are included. Each event is stamped at the last millisecond of that UTC day.
  The on-disk summary is removed on opt-out and when the install ID is retired.
- Renderer API errors (`ao.requests.failed`) are not sampled. They carry the
  normalized route (identifiers replaced by `:id`), HTTP status, a category,
  and the daemon's stable `error_code` when it sent one. The desktop no longer
  reads a session's workspace before its worktree exists, which was the most
  common 404. A daemon-unavailable report is skipped
  while the daemon is still starting. HTTP 409 responses are dropped only for
  `POST /api/v1/sessions/:id/workspace/diffs`,
  `GET /api/v1/sessions/:id/conversation/config-options`,
  `GET /api/v1/sessions/:id/conversation/models`, and
  `GET /api/v1/sessions/:id/conversation/skills`. Codex
  account management being unwired, and an already-closed shell terminal, are
  not analytics events. A GitHub token rejected after refresh still reports
  `GITHUB_AUTH_INVALID` (with that `error_code`). A temporary refresh outage reports
  `GITHUB_REFRESH_UNAVAILABLE` for retry instead of claiming the credential is
  invalid. A Codex login
  terminal failure is `CODEX_LOGIN_TERMINAL_UNAVAILABLE`. An incomplete Codex
  account setup is `CODEX_ACCOUNT_SETUP_INCOMPLETE`. Both still report on the
  account routes. The account event stream stays open for setup-incomplete
  and for account management that is not wired in this process, so those
  conditions do not reconnect in a loop. A failed project add emits
  `ao.projects.add_failed` with `error_code`. A failed project clone
  emits `ao.projects.clone_failed` with `error_code`. The code is the
  daemon's API error code, or `DAEMON_NOT_READY`, `REQUEST_FAILED` or
  `NO_PROJECT` when the request never reached a usable response. A clone the
  user cancels emits nothing.
- An updater failure streak emits one `ao.updates.failed` until a
  successful download of that target, or a successful check for a check-phase
  streak, clears it. Retries in the streak do not emit again. Each install
  also emits one `ao.usage.update_failures_daily` event, with the UTC day
  and failure count, after that day ends. A rejected capture leaves the streak
  unsent. A restart keeps the open streak.
- `ao.reviews.triggered` counts a newly created or restarted run and does not
  include a `reused` property. Reused sweeps are not events, and
  `trigger_failed` is no longer a subset of `triggered`. The series breaks on
  the merge of this change. Issue #4665 previously counted every attempt.
- Session recording is disabled in the desktop and mobile apps. Orchestrator.inc does not
  automatically record screens, clicks, or touches.
- The desktop app loads PostHog feature flags so experiments can target an
  installation. Flags load only while Share usage analytics is on: an
  opted-out launch never starts the PostHog client, and turning analytics off
  during a session stops flag requests until it is turned back on. Flag
  requests carry no page address or referrer: the desktop does not let the
  PostHog SDK store the first page it saw.
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

When you sign in to AO Cloud, the desktop app hands the AO Cloud user ID to the
daemon, which records it (`telemetry_bound_user`), and then calls PostHog
`identify` with your AO Cloud user ID as the identity and sets three person
properties:
`email`, `ao_cloud_user_id`, and `github_login`. The initial anonymous
`distinct_id` is merged into that identity on first sign-in. While you stay
signed in, desktop, daemon and CLI events all use the AO Cloud user ID as
`distinct_id`: the desktop starts each launch identified when its stored
sign-in matches the account the daemon recorded, and the daemon keeps the
recorded account across restarts, including a daemon the CLI started. Signing
out moves the desktop and the daemon to a new anonymous identifier, so later
signed-out activity is not attributed to the account; signing in again
returns to the AO Cloud user ID. `install_id` stays the same throughout. A paired phone keeps its own
identity and carries the Cloud user ID only as an event property. Account fields
on events are the user ID and, once resolved, the
GitHub username (as `ao_cloud_user_id` and `github_actor`). The installation
properties described above are also event fields. The email is a person
property only. It comes from the AO Cloud
account you signed in with and is never read from git config. The daemon and the
mobile app never receive your email; they receive only the user ID. Signing out
stops attributing new desktop events to you. Only the production WorkOS sign-in
is identified.

A release build returns from the sign-in page through
`https://orchestrator.inc/app/auth/return/` on the Orchestrator.inc website.
If you accepted analytics on that website, the page passes your website
PostHog `distinct_id` to the desktop app along with the sign-in result. The
desktop app uses it only when Share usage analytics is on: right after it
identifies you, it calls PostHog `alias` once, which links your website visits
to your AO Cloud person, and sends one `ao.renderer.cloud_web_visitor_link_requested`
event carrying that website ID as `website_distinct_id`. The event records
that the link was requested: PostHog can still refuse the merge, for example
when that website ID already belongs to an identified person. This can link your
website visit to your AO Cloud account. The website ID plays no part in signing
in, is not written to disk, settings, or logs, and is not sent again on later
launches, token refreshes, or a repeated return link. If you did not accept
website analytics, or desktop analytics are off, nothing is linked. Builds that
sign in against another WorkOS environment, and development builds, return
through other pages that never add a website ID.

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
only: the daemon's Sentry capture of reportable 5xx errors, registered fault
codes answered as 4xx or 503, background failures, and handler or background
goroutine panics; and Electron main capture of process crashes, handled
daemon, update and IPC failures, uncaught exceptions, unhandled rejections,
and reportable renderer errors. Sentry
receives fixed failure or panic labels and allowlisted error codes instead of
raw error messages. It groups HTTP faults by registered route and stable error
code, or by a bounded cause type when no code exists. Ordinary Go errors carry
no synthetic throw stack. Recovered panics retain the interrupted AO function.
Agent relaunches group by operation and failure step, with a fixed step tag
and no HTTP route tag.
An event may include a bounded `git`, `db`, `agent`, or `network` area when the
error provides evidence. Panic events keep function and line information, but
remove stack paths, request data, project, session, repository and branch
names, and panic text. Electron exception titles use a standard error type and
a fixed function name or bounded bundle location; renderer API titles use a
fixed error code or category.
Both have scrubbed exception values and the same
installation ID as Sentry `user.id`.
Canceled requests, plain transient 503 responses, and expected upstream link-preview
502 responses do not create Sentry issues. A 503 caused by a server deadline or
a registered daemon fault code is reportable. Registered route templates remain
available for diagnosis. Unmatched URLs are reported as `unmatched` instead of
their raw path. Every daemon Sentry event has release and environment values.
Typed daemon 503 envelopes remain breadcrumbs in the renderer, matching daemon
Sentry policy. Untyped renderer 503 responses remain reportable. The existing
API client skips its confirmed expected 409 probes, while other 409 responses
remain ordinary 4xx breadcrumbs. Renderer exceptions are sampled and capped in
Electron main as described under Sampling and daily caps. HTTP and background daemon sends
share a limit of four concurrent reports, dropping diagnostics at capacity without
retaining a queue. Their transport runs outside the caller's locks and database
transactions, and opt-out waits for admitted sends before it completes.
The daemon admits the first five
reportable errors per fingerprint each minute and at most 200 per day. Its
in process budget tracks at most 1,024 distinct fingerprints per day, then
uses one overflow group per operation. On top of that, the daemon sends
non-fatal errors only from sampled installs and at most 25 per install per UTC
day; panics always send.
Model discovery failures (a missing, outdated or misconfigured agent CLI,
including a non-zero exit) and an unfinished Codex account setup
(`CODEX_ACCOUNT_SETUP_INCOMPLETE`, even when its staging step hits a deadline)
are user setup states and never create Sentry issues.
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

## Event names

Event names follow `{product}.{object}.{past_tense_verb}`, where the product is
`ao` (desktop app, daemon and CLI) or `mobile`, and the object is plural.
Rollups stay under `ao.usage.*`. Which process sent an event is the `client`
and `surface` properties, and the schema is `telemetry_schema_version`; neither
is part of the name. Desktop, CLI and phone activity use `ao.app.active`,
`ao.cli.active` and `mobile.app.active`, each at most once per UTC day. For
total active users, count distinct people across the three, not the sum of
events: someone active on two surfaces on the same day is one user. The current
names carry `telemetry_schema_version = 3`; legacy clients sent
`ao.cli.invoked` and `ao.app.active` under the same names without a version, so
filter on the version when reading those two. See
[PostHog cost controls](posthog-cost-controls.md) for the ingestion rules.

Events were renamed in this release. A kill-switch entry in
`AO_TELEMETRY_DISABLED_EVENTS` written against an old name still silences the
renamed event. Events not listed keep their names.

| Old name | New name |
| --- | --- |
| `ao.session.spawned` | `ao.sessions.spawned` |
| `ao.session.spawn_failed` | `ao.sessions.spawn_failed` |
| `ao.session.ended` | `ao.sessions.ended` (already shipped in 0.14.9) |
| `ao.session.waiting_input_entered` | `ao.sessions.input_wait_started` |
| `ao.session.waiting_input_exited` | `ao.sessions.input_wait_ended` |
| `ao.session.heartbeat_stale` | app log only (already not sent) |
| `ao.onboarding.first_session_spawned` | `ao.sessions.first_spawned` |
| `ao.onboarding.second_session_spawned` | `ao.sessions.second_spawned` (not sent; use `lifetime_sessions: 2` on `ao.sessions.spawned`) |
| `ao.renderer.session_kill_requested` | dropped (already not sent) |
| `ao.renderer.session_kill_succeeded` | `ao.sessions.killed` |
| `ao.renderer.session_kill_failed` | `ao.sessions.kill_failed` |
| `ao.renderer.orchestrator_open_requested` | dropped (already not sent) |
| `ao.renderer.orchestrator_spawn_requested` | dropped (already not sent) |
| `ao.renderer.orchestrator_spawn_succeeded` | `ao.orchestrators.spawned` |
| `ao.renderer.orchestrator_spawn_failed` | `ao.orchestrators.spawn_failed` |
| `ao.orchestrator_reengagement.backstop_sent` | app log only (already not sent) |
| `ao.review.triggered` | `ao.reviews.triggered` |
| `ao.review.submitted` | `ao.reviews.submitted` |
| `ao.review.trigger_failed` | `ao.reviews.trigger_failed` |
| `ao.review.cancelled` | `ao.reviews.cancelled` |
| `ao.projects.created` | `ao.projects.created` (unchanged) |
| `ao.renderer.project_add_requested` | dropped (already not sent) |
| `ao.renderer.project_add_succeeded` | `ao.projects.added` |
| `ao.renderer.project_add_failed` | `ao.projects.add_failed` |
| `ao.renderer.project_clone_failed` | `ao.projects.clone_failed` |
| `ao.renderer.project_removed` | `ao.projects.removed` |
| `ao.onboarding.first_project_added` | `ao.projects.first_added` |
| `ao.renderer.task_create_requested` | dropped (already not sent) |
| `ao.renderer.task_create_succeeded` | `ao.tasks.created` |
| `ao.renderer.task_create_failed` | `ao.tasks.create_failed` |
| `ao.renderer.settings_save_requested` | dropped (already not sent) |
| `ao.renderer.settings_save_succeeded` | `ao.settings.saved` |
| `ao.renderer.notification_mark_read_requested` | dropped (already not sent) |
| `ao.renderer.notification_mark_read_succeeded` | `ao.notifications.read` |
| `ao.renderer.notification_opened` | `ao.notifications.opened` |
| `ao.renderer.open_in_editor_requested` | dropped (already not sent) |
| `ao.renderer.open_in_editor_succeeded` | `ao.editor.opened` |
| `ao.renderer.automation_create_opened` | `ao.automations.create_opened` |
| `ao.renderer.terminal_attach_failed` | `ao.terminals.attach_failed` |
| `ao.renderer.support_opened` | `ao.support.opened` |
| `ao.renderer.update_failed` | `ao.updates.failed` |
| `ao.renderer.update_failures_daily` | `ao.usage.update_failures_daily` |
| `ao.renderer.update_downloaded` | `ao.updates.downloaded` |
| `ao.renderer.agents_available` | app log only |
| `ao.renderer.cloud_offering_resolved` | app log only |
| `ao.renderer.mobile_connect_opened` | `ao.mobile.connect_opened` |
| `ao.renderer.mobile_bridge_toggled` | `ao.mobile.bridge_toggled` |
| `ao.mobile.device_connected` | `ao.mobile.device_connected` (unchanged) |
| `ao.v2.renderer.api_error` | `ao.requests.failed` |
| `ao.v2.renderer.daemon_failure` | `ao.daemon.failed` |
| `ao.v2.renderer.loaded` | `ao.app.loaded` |
| `ao.v2.renderer.route_viewed` | `ao.routes.viewed` |
| `ao.v2.renderer.session_management_summary` | unchanged |
| `ao.v2.app.active` (desktop) | `ao.app.active` |
| `ao.v2.app.active` (CLI) | `ao.cli.active` (new name, same shape) |
| `ao.v2.app.active` (mobile) | `mobile.app.active` (new name, same shape) |
| `ao.v2.cli.invoked` | `ao.cli.invoked` |
| `ao.cli.usage_errors` | `ao.usage.cli_usage_errors` |
| `ao.cli.first_used` | `ao.cli.first_used` (unchanged) |
| `ao.daemon.started` | `ao.daemon.started` (unchanged) |
| `ao.harness.update` | `ao.harnesses.updated` |
| `ao.harness.version_switch` | `ao.harnesses.version_switched` |
| `ao.harness.stale_sessions` | app log only (already not sent) |
| `ao.app.installed` | `ao.app.installed` (fresh installs only) |
| `ao.install.created` | removed; folded into `ao.app.installed` |
| `ao.app.first_open` | `ao.app.first_opened` |
| `ao.onboarding.app_opened` | `ao.app.first_opened` |
| `ao.onboarding.cli_detected` | `ao.agents.detected` (not sent; shipped as `ao.cli.first_used`, unchanged) |
| `ao.onboarding.next_day_return` (shipped as `ao.app.next_day_returned`) | dropped; computed in PostHog retention |
| `ao.setup.agent_missing` | `ao.agents.missing` (already shipped) |
| `ao.setup.agent_install_clicked` | `ao.agents.install_clicked` (already shipped) |
| `ao.onboarding.step_viewed` | unchanged |
| `ao.onboarding.step_completed` | unchanged |
| `ao.v2.mobile_app.connected` | `mobile.app.connected` |
| `ao.v2.mobile_app.paired` | `mobile.app.paired` |
| `ao.v2.mobile_app.onboarding_started` | `mobile.app.onboarding_started` |
| `ao.v2.mobile_app.onboarding_completed` | `mobile.app.onboarding_completed` |
| `ao.v2.mobile_app.onboarding_skipped` | `mobile.app.onboarding_skipped` |
| `ao.v2.mobile_app.feature_used` | `mobile.app.feature_used` |
| `ao.v2.mobile_app.notification_opened` | `mobile.app.notification_opened` |
| `ao.usage.*` (`worker_pr_associated`, `worker_pr_merged`, `messages_sent`) | unchanged |
| `ao.usage.worker_pr_closed` | not shipped under this name; the shipped event `ao.prs.closed` is unchanged |
| `ao.usage.daily_summary` | not shipped; nothing to rename |
| `ao.http.5xx` | unchanged |
| (new) | `ao.setup.checked` |


## Questions or corrections

For the broader data policy, retention information, and contact options, see
the [Orchestrator.inc privacy policy](https://orchestrator.inc/privacy). You can report a problem
with this documentation in the
[GitHub repository](https://github.com/OrchestratorInc/agent-orchestrator).

Handled Electron main captures are limited per sanitized category, reason and detail to five per minute and 200 per UTC day, with at most 1,024 budget keys per day. Pending pre-policy captures count against the same limits. Renderer operation tags are forwarded only for fixed exception operations. Remote proxy connectivity and identity-verification refusals are expected states.
