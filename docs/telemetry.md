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
never an install path. Desktop and mobile Sentry reports use the application
error code when available. Uncoded JavaScript engine errors such as TypeError,
ReferenceError, RangeError, SyntaxError, and DOMException can include the first
line of the message after paths, URLs, emails, identifiers, quoted values, and
numbers are removed, capped at 120 characters. Message text is withheld when
its quoted parts cannot be separated reliably. Long engine messages and other
uncoded errors use a computed title. All uncoded errors group by a short
SHA-256 hash of the locally normalized first line.
The app's global renderer error boundary binds handlers independently of
PostHog setup. Electron main still checks Share error events before sending.
Preload installs main-world error and rejection handlers before renderer imports
and reads the consent policy itself. An import failure can therefore report even
when the renderer never starts. Early reports use the same error codes and message rules, with a standard
exception type and validated asset locations. Once the renderer starts,
the same listeners hand off to its normal reporter. Early captures are not
replayed through the later renderer boundaries.
Router error boundaries and query/mutation cache callbacks also report unexpected
client failures. The sender excludes query keys, mutation inputs and route
parameters. API failures retain a local ownership marker through message wrapping,
so these callbacks do not duplicate the API client's report or escalate expected
API state. Repeated observations of the same exception object are reported once.
Background side-chat restore and snapshot failures use the same main-owned
consent gate and message rules, with no launch-state payload. Expected connection,
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

Mobile Sentry sends application codes and applies the same normalized first-line display rule for JavaScript engine errors. Other uncoded errors contribute only a hash of the cleaned-up text. It also receives the error category, safe stack line numbers with generic
frame names, release version and build number, the running OTA update ID
(`embedded` for the bundle in the binary) as `dist` and `ao.update_id`, the
daemon's version as `ao.daemon_version`, and the production or preview
environment. For a failed request that is the version of the desktop the
request went to; for other errors it is sent only when every connected desktop
reported its version and they are all the same. A failed daemon request is named by its method and route as
`ao.operation`, such as `POST /api/v1/sessions/:id/send`: only the app's fixed
route words are kept, every other path segment becomes `:id`, and a workspace
file path becomes `*`, so no session, project, branch or file name is sent.
The request ID is not sent. The sender excludes request bodies and account identity. Its `user.id` is the authenticated paired
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
  Failed onboarding installs and sign-ins also send from every install (see
  "Onboarding errors in Sentry"): they are rare and block a new user's first
  run.
- Every event carries a `sample_rate` tag: `0.1` for sampled non-fatal events
  and `1` for always-sent events. Multiply counts of `sample_rate:0.1`
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
- Daemon startup time: once the daemon's API starts listening,
  `ao.daemon.ready` sends `startup_ms`, the milliseconds from the daemon
  process starting, split into back-to-back steps that add up to it:
  `prepare_ms`, `lock_wait_ms` (waiting for a previous daemon to release the
  data directory), `store_open_ms` (opening and migrating the database),
  `lifecycle_setup_ms`, `chat_setup_ms`, `session_setup_ms`,
  `install_recovery_ms` (agent installs left by a previous run),
  `services_setup_ms`, `codex_recovery_ms`, `startup_reconcile_ms` (the
  session safety check that must finish before the API opens), and
  `listen_ms`. When the restore that starts alongside the API ends,
  `ao.daemon.startup_reconciled` sends `reconcile_ms` split into
  `chat_hosts_ms`, `reviewer_recovery_ms`, `sessions_ms`, and `runtime_ms`.
  Both carry durations only
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
- Desktop onboarding progress: each step viewed, completed, skipped or left,
  installs and sign-ins started there and how they ended, how long onboarding
  took, and whether the first chat message followed. See "Desktop onboarding
  funnel" below. Also a one-time install event on first run
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
  opening a notification, or using a core action. Phone events also carry the
  running OTA update as `update_id` (a UUID, or `embedded` for the bundle in the
  binary) and `update_channel` (`production`, `preview`, `development`, or
  `other`), and while a desktop is paired its daemon's version as
  `paired_daemon_version` (the desktop named by `paired_install_id`; with
  several connected, the one whose identity the phone adopted).
  `notification_opened` reports its `target` as `session`, `review`, `prs`, or
  `cloud`. `paired` is sent for every pairing finished from Pair a machine, by
  scan or manual entry: a new machine, a re-pair, or an edited connection. A
  manual pairing is reported even if the person left the page while it
  connected.
  The phone also reports which of its screens are used and how long its
  launches take (see "Mobile app screens and launches" below)
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

## Artifacts

When product telemetry is enabled, Orchestrator.inc records visits to the
Artifacts page with `ao.routes.viewed` and `surface: artifacts`, and the events
below: how artifacts are created, and how people publish them and copy their
links. They carry only the closed values listed here and counts. They never
include a file name, path, extension, URL, artifact content, title, session or
project name, raw error message, or the people a link is shared with.

| Event | When it is sent | Artifact-specific properties |
| --- | --- | --- |
| `ao.artifacts.session_started` | New artifact on the Artifacts page starts its session (formerly `ao.renderer.artifact_create_succeeded`) | `scope` |
| `ao.artifacts.session_start_failed` | Starting that session fails (formerly `ao.renderer.artifact_create_failed`) | `scope`, `error_code`, `http_status` |
| `ao.artifacts.registered` | The daemon registers new artifacts for a session (`ao artifact add`). One event per content category and size band among them; registering a path again sends nothing | `source`, `content_category`, `size_bucket`, `count` |
| `ao.artifacts.register_failed` | The daemon refuses an `ao artifact add` | `source`, `reason` |
| `ao.artifacts.publish_clicked` | The Publish popover opens: you click the toolbar's Publish, Published or Republish button, or Publish in the ⋯ menu, before any sign-in check or upload. Opening it on a published file to copy the link counts too, with `already_published` true | `already_published`, `signed_in` |
| `ao.artifacts.sign_in_prompt_shown` | The Publish popover asks you to sign in, once per popover | `reason` |
| `ao.artifacts.sign_in_clicked` | You click its Sign in button | none |
| `ao.artifacts.published` | A publish or republish succeeds | `is_republish` |
| `ao.artifacts.publish_failed` | A publish attempt fails | `is_republish`, `reason` |
| `ao.artifacts.publish_cancelled` | You cancel a publish before its upload finishes | `is_republish` |
| `ao.artifacts.publish_blocked` | The Publish popover opens on a refusal, before any attempt: the link limit, or a file type that cannot be published yet (once per popover) | `is_republish`, `reason` |
| `ao.artifacts.link_copied` | You copy a published link | `surface` |
| `ao.artifacts.unpublished` | Unpublishing succeeds | none |

- `scope` is `standalone` for the Scratchpad, `project` for a project, and
  `cloud` for an AO Cloud project.
- `error_code` is the API's error code (such as `AGENT_NOT_READY`), never its
  message; `http_status` is the 4xx or 5xx status when the daemon refused.
- `source` is `artifacts_tab` for a worker started by New artifact, otherwise
  `worker` or `orchestrator`: the kind of session that ran `ao artifact add`.
- `content_category` comes from the file's extension: `markdown`, `html`,
  `image`, `video`, `audio`, `pdf`, `text`, or `other` (a bundle directory is
  `other`).
- `size_bucket` is `xs` (under 10 KB), `s` (under 100 KB), `m` (under 1 MB),
  `l` (under 10 MB), or `xl`. A bundle directory's size is that of the files in
  it. It is omitted when the daemon could not list the files.
- `reason` on `register_failed` is `outside_dir` (not in the session's
  artifact directory), `not_found`, `not_registrable` (not a file or bundle
  directory), `session_not_found` (the session was deleted while its artifacts
  were being registered), or `internal`. A command that fails before it reaches
  the daemon is not counted here.
- `registered` is not sent when the daemon cannot read which paths the session
  had already registered, since it could not tell new artifacts from old ones.
  Those registrations go uncounted.
- `already_published` is whether the file had an active link when Publish was
  clicked; `signed_in` is whether you were signed in to AO Cloud.
- `reason` on `sign_in_prompt_shown` is `signed_out` (signed out when you
  clicked Publish) or `session_expired` (a publish came back signed out).
- `is_republish` is true when an existing link was being updated.
- `reason` on `publish_failed` is `signed_out`, `too_large`, `upload_failed`
  (the upload, or reading the file from its machine, failed), `refused` (the
  server refused the upload: the plan's link limit, a file type that cannot be
  published yet, or someone else's cloud session), or `other`. Cancelling is
  `publish_cancelled` and a refusal shown on open is `publish_blocked`, so
  neither counts as a failure.
- `reason` on `publish_blocked` is `limit` or `unsupported`.
- `surface` on `link_copied` is `panel` (the Publish popover) or `menu` (the
  artifact's ⋯ menu).

`ao.renderer.cloud_signed_in` also carries `source`: `artifact_publish` when
the sign-in finished within 30 minutes of clicking Sign in in the Publish
popover, while the popover stayed open, and `other` otherwise. That note is
kept in memory only. It also carries `same_account`: true when you signed in
again as the account that was already signed in, as after a server refused an
expired session. Those sign-ins were not counted before, so filtering on
`same_account` not true reproduces the earlier count. A token refresh is not a
sign-in. Together these events give the sign-in drop-off funnel:
`publish_clicked` (`signed_in` false) → `sign_in_prompt_shown` →
`sign_in_clicked` → `cloud_signed_in` (`source` `artifact_publish`) →
`published` → `link_copied`.

The `ao.artifacts.registered` and `ao.artifacts.register_failed` events are
sent by the daemon that registered the file, so they follow that daemon's
telemetry setting; cloud sessions register artifacts in AO Cloud and do not
send them. The other events are sent by the desktop app at most once per
action or per open, never on re-renders, and stop when Share usage analytics
is off. A kill-switch entry written against an old event name still silences
the renamed event.

The automation events do not include the prompt text, automation name, raw
project identifier, exact schedule, timezone, selected agent, or error message.
Editing an existing automation does not emit these creation events. Closing the
create dialog or failing local validation does not emit a separate event.
Orchestrator.inc uses the outcome events to understand which schedule presets
people choose and whether creation succeeds.

The desktop product-telemetry controls described under "Turn desktop and daemon
telemetry off" also control these events.

## UI usage

When desktop product telemetry is enabled, Orchestrator.inc records which parts
of the interface people look at and use, so we can tell which surfaces earn
their space, which controls nobody finds, and where people get stuck. The
properties specific to these events are only the closed values listed here,
control ids, counts and durations. Like every desktop event, they also carry
the shared context described under What Orchestrator.inc sends, such as
`install_id` and `days_since_install`. They never include text you typed or
clicked, a file name, path, URL, session or project name, chat content, error
message, click position, window size, DOM path, or screenshot. There is no
heatmap and no session replay.

| Event | When it is sent | Properties |
| --- | --- | --- |
| `ao.surfaces.viewed` | A surface stays on screen for a second. At most once per surface per UTC day | `surface`, `opened_by` |
| `ao.features.first_used` | The first deliberate use of a feature on this install. At most once per feature | `feature` |
| `ao.ui.usage_summary` | Every 45 to 60 minutes of app time, or at the next launch when a window ended while the app was closed. Not sent when nothing was counted | Window fields, counters, `surfaces`, `controls` (below) |
| `ao.ui.error_shown` | A user-visible error renders: an error toast, the daemon banner, a failed chat turn, or a composer send failure. The same error shown again within 5 minutes is counted in the summary but not sent again | `presentation`, `error_kind`, `surface`, `error_code` |
| `ao.ui.rage_clicked` | The first rage click on a surface each UTC day | `surface` |

### Closed values

- `surface` is one of `sidebar`, `topbar` (the top bar and its tabs), `chat`
  (the transcript), `composer`, `new_chat`, `board`, `pr_tab`,
  `inspector_summary`, `inspector_reviews`, `inspector_browser`,
  `inspector_device`, `inspector_files`, `inspector_artifacts`, `inspector_side_chat`,
  `inspector_subagents`, `inspector_background_tasks`, `cmdk`,
  `notifications_panel`, `archive_modal`,
  `model_picker`, `slash_menu`, `mention_menu`, `split_view`, `terminal`,
  `onboarding`, `toast`, or a settings page: `settings_general`,
  `settings_agents`, `settings_teams`, `settings_billing`, `settings_browser`,
  `settings_computer_use`, `settings_coder`, `settings_remote_hosts`,
  `settings_mobile`, `settings_shortcuts`, `settings_diagnostics`,
  `settings_updates`, `settings_help`, or `settings_project`. A settings page
  added later reports as `settings_general` until it is listed.
- `opened_by` is `click`, `keyboard` (Enter, Space, Escape, Tab or an arrow key
  on a focused control), `shortcut` (a key chord or a swipe gesture), `cmdk`
  (a Cmd+K command), `restore` (on screen within 10 seconds of launch, such as
  a restored layout), or `auto` (opened by the app, with no input in the 1.5
  seconds before).
- `feature` is `cmdk`, `slash_menu`, `mention_menu`, `model_change`,
  `attachment`, `steer`, `queue`, `keyboard_send`, `split_pane`,
  `terminal_tab`, `side_chat`, `board`, `pin_session`, `archive_modal`,
  `dictation`, `swipe_navigation`, `notifications_panel`, `inspector_summary`,
  `inspector_reviews`, `inspector_browser`, `inspector_device`,
  `inspector_files`, `inspector_artifacts`, `inspector_subagents`, or
  `inspector_background_tasks`. Opening a surface counts as
  using its feature only when you opened it (`opened_by` is not `restore` or
  `auto`). The marker is kept in the app's local storage, so an install that
  used a feature before this release reports it again the first time it is
  used after updating; read `first_used` as "first use since this release" for
  those installs.
- `presentation` is `toast`, `banner`, `inline`, or `dialog`.
- `error_kind` is `toast` (any error toast), `daemon_unavailable`,
  `chat_turn_failed`, `composer_send_failed`, or `other`.
- `error_code` is one of a closed list, never a message: a daemon failure
  (`NOT_CONFIGURED`, `DAEMON_UNREACHABLE`, `BINARY_MISSING`, `SPAWN_FAILED`,
  `EXITED`, `PORT_UNCONFIRMED`, `NOT_READY`, `IDENTITY_MISMATCH`,
  `DATADIR_UNWRITABLE`) or a chat turn failure (`AUTH_REQUIRED`,
  `PROVIDER_KEY_MISSING`, or `USAGE_LIMIT` when the agent's account hit its
  usage limit). Any other chat turn failure is named by matching its error text,
  in the app, against fixed patterns: `QUOTA_EXCEEDED`, `RATE_LIMITED`,
  `PROVIDER_OVERLOADED`, `CONTEXT_TOO_LONG`, `MODEL_UNAVAILABLE`,
  `NETWORK_ERROR`, `TIMED_OUT`, `PROVIDER_SERVER_ERROR`, `AGENT_NOT_INSTALLED`,
  `AGENT_EXITED`, or `CONTENT_REFUSED`. The error text itself is never sent. A
  failed composer send is one of the app's own reasons (`ANNOTATION_LIMIT`,
  `EXCERPT_TOO_LONG`, `MESSAGE_TOO_LONG`, `FILE_REFERENCES_UNSUPPORTED`,
  `ATTACHMENTS_UNAVAILABLE`, `ATTACHMENT_UNREADABLE`, `COMPACT_UNAVAILABLE`,
  `COMPACT_FAILED`, `CONTINUE_FAILED`, `QUEUE_SAVE_FAILED`, `SEND_FAILED`,
  `SEND_REJECTED`, `UNSENT_RETRY_FAILED`) or the API code that refused the send,
  in upper case (`AGENT_SWITCH_FAILED`, `TASK_CREATE_FAILED`,
  `SESSION_RESTORE_FAILED`, `CHAT_FILE_REFERENCE_INVALID`,
  `CHAT_ANNOTATION_LIMIT`, `CHAT_FILE_REFERENCES_SIDE_UNSUPPORTED`,
  `VALIDATION_ERROR`, `INVALID_BODY`, `CHAT_MESSAGE_EMPTY`, `INVALID_RESOURCE`,
  `UNSUPPORTED_ATTACHMENT_TYPE`, `INVALID_ATTACHMENT_DATA`,
  `ATTACHMENT_TOO_LARGE`, `TOO_MANY_ATTACHMENTS`, `ATTACHMENTS_TOO_LARGE`,
  `SESSION_NOT_FOUND`, `SESSION_MODE_MISMATCH`, `CHAT_CONTROLLER_NOT_READY`,
  `CHAT_RESUME_FAILED`, `CHAT_INTERFACE_TRANSITION`, `CHAT_OPEN_ELSEWHERE`,
  `COMPOSER_LOCKED`, `COMPOSER_LEASE_LOST`, `CHAT_EXCERPT_INVALID`,
  `CHAT_EXCERPT_STALE`, `CHAT_SIDE_UNAVAILABLE`, `CHAT_SIDE_UNSUPPORTED`,
  `CHAT_SIDE_ANCHOR_UNAVAILABLE`, `CHAT_SIDE_INVALID_REQUEST`,
  `CHAT_SIDE_NOT_READY`, `CHAT_SIDE_RECREATE_REQUIRED`, `CHAT_SIDE_CLOSED`,
  `CHAT_SIDE_IDEMPOTENCY_CONFLICT`). Any other code is dropped, and it is
  omitted when there is none or no pattern matches.
- `surface` on `ao.ui.error_shown` is the surface that showed the error, or,
  for a toast, the surface you used in the 10 seconds before it. It is omitted
  when neither is known.
- A failed chat turn is reported when it renders within two minutes of failing,
  once per turn while the app runs, so opening an old chat does not report its
  past failures.

### `ao.ui.usage_summary`

Window fields:

| Property | Meaning |
| --- | --- |
| `measurement_schema_version` | `2`. In `1`, `shortcut_count` counted every key chord the page saw rather than app shortcuts, and there was no `shortcuts` list |
| `window_id` | A random UUID for this window, not tied to anything else |
| `window_duration_seconds` | Wall time from the window's start to the send |
| `active_seconds` | Time the window was focused and visible, with input in the last 5 minutes |
| `flush_reason` | `interval`, or `startup` for a window that ended while the app was closed |

Counters (a counter that stayed at 0 is left out):

| Property | Counts |
| --- | --- |
| `session_switch_count` | Switches from one open session to another |
| `switch_from_<source>_count` | Those switches by where they started. `<source>` is `sidebar`, `tab`, `cmdk`, `shortcut`, `notification`, `board`, `swipe`, `archive`, or `other` (no input in the 1.5 seconds before) |
| `send_count`, `send_keyboard_count`, `send_button_count` | Messages sent from a composer, all and by Enter or the send button |
| `steer_count`, `queue_count` | Sends that steered a running turn or were queued behind it |
| `send_with_attachment_count` | Sends with at least one attachment |
| `attachment_add_paste_count`, `attachment_add_drop_count`, `attachment_add_picker_count` | Attachments added by paste, drag and drop, or the file picker |
| `composer_draft_discarded_count` | A draft of 10 or more characters erased without sending while the composer had focus |
| `model_picker_change_count` | Model changes made in the composer's model picker |
| `cmdk_open_count`, `cmdk_pick_count`, `cmdk_abandon_count` | Cmd+K opens, opens that ran a command, and opens closed without one |
| `model_picker_open_count`, `model_picker_abandon_count` | Model picker opens and opens closed without a change |
| `slash_menu_open_count`, `slash_menu_pick_count`, `slash_menu_abandon_count` | The composer's `/` menu |
| `mention_menu_open_count`, `mention_menu_pick_count`, `mention_menu_abandon_count` | The composer's `@` menu |
| `notifications_panel_open_count` | Notification panel opens |
| `click_count`, `keyboard_activation_count` | Pointer clicks, and clicks made with Enter or Space on a focused control |
| `shortcut_count` | App shortcuts run by their key chord, all of them (each one is in `shortcuts`). Running one from Cmd+K or the menu bar is not counted here |
| `hover_click_count`, `hover_only_count` | On controls with a hover card or preview: clicks after hovering for at least 500 ms, and hovers of 500 ms or more that ended without a click |
| `rage_click_count` | Rage clicks (below) |
| `dead_click_count` | Dead clicks (below) |
| `error_shown_count`, `error_toast_count` | User-visible errors, all and toasts only |
| `retry_click_count` | Clicks on a Retry control after a failure |

`surfaces` is a list with one entry per surface that had any activity in the
window. Each entry has `surface` and any of these that are above 0:

| Property | Meaning |
| --- | --- |
| `visible_seconds` | Time the surface was on screen while the window was active |
| `focused_seconds` | Time keyboard focus was inside the surface while the window was active |
| `open_count` | Times it came on screen. A surface back on screen within a second of leaving it, such as a panel that re-renders, is the same open. Menus and pickers count every open |
| `pick_count`, `abandon_count` | For menus and pickers (`cmdk`, `model_picker`, `slash_menu`, `mention_menu`, `notifications_panel`, `archive_modal`): closes after a choice, and closes without one |
| `dismiss_count` | Explicit dismissals, such as closing a toast |
| `dead_click_count`, `rage_click_count` | Dead and rage clicks inside it |
| `error_count`, `retry_count` | Errors attributed to it and Retry clicks inside it |

`controls` is a list with one entry per named control used in the window.
Each entry has `control_id` and any of `click_count`, `keyboard_count`,
`shortcut_count`, `hover_click_count`, `hover_only_count`, and
`rage_click_count` that are above 0. `shortcut_count` is the key chord of the
shortcut that does the same thing as the control, so clicks and shortcuts can
be compared: `topbar.new_task` (`new-session`), `sidebar.settings`
(`open-settings`), `sidebar.collapse` (`toggle-sidebar`), `topbar.inspector`
(`toggle-inspector`), `topbar.open_orchestrator` (`open-orchestrator`),
`topbar.open_editor` (`open-in-editor`), `sidebar.session_archive`
(`archive-session`), `composer.model_picker` (`model-picker`),
`composer.dictation` (`dictate`), `tab.close` (`close-shell-terminal`),
`sidebar.session_row` (the session switching shortcuts) and `tab.select`
(`previous-tab`, `next-tab`). `control_id` is a stable product name from a fixed list, not a DOM path or
label:

- Sidebar: `sidebar.home`, `sidebar.collapse`, `sidebar.new_project`,
  `sidebar.settings`, `sidebar.connect_mobile`, `sidebar.project_row`,
  `sidebar.project_toggle`, `sidebar.project_new_session`,
  `sidebar.project_orchestrator`, `sidebar.project_menu`, `sidebar.project_unpin`, `sidebar.session_row`,
  `sidebar.session_pin`, `sidebar.session_archive`,
  `sidebar.archived_sessions`
- Tabs and top bar: `tab.select`, `tab.close`, `tab.new`,
  `topbar.notifications`, `topbar.inspector`, `topbar.new_task`,
  `topbar.open_board`, `topbar.open_orchestrator`, `topbar.open_editor`
- Composer: `composer.send`, `composer.stop`, `composer.attach`,
  `composer.model_picker`, `composer.dictation`
- Chat: `chat.retry`
- Inspector tabs: `inspector.tab_summary`, `inspector.tab_reviews`,
  `inspector.tab_browser`, `inspector.tab_device`, `inspector.tab_files`, `inspector.tab_artifacts`,
  `inspector.tab_side_chat`, `inspector.tab_subagents`,
  `inspector.tab_background_tasks`
- Board: `board.card`
- Toasts and banners: `toast.action`, `toast.dismiss`, `daemon_banner.retry`

Hover counts are kept for `sidebar.session_row`, `tab.select`, and
`board.card`. Retry clicks are `chat.retry` and `daemon_banner.retry`.

`shortcuts` is a list with one entry per app shortcut used in the window. Each
entry has `shortcut_id` and any of `key_count` (run by its key chord) and
`cmdk_count` (run from Cmd+K) that are above 0. Which keys were pressed, and
any custom binding, are never sent. Running a shortcut's action from the menu
bar is not counted. `shortcut_id` is the shortcut's id in the keyboard
shortcuts list: `find-in-chat`, `add-selection-to-chat`, `new-session`,
`command-palette`, `quick-open-file`, `open-settings`, `keyboard-shortcuts`,
`previous-session`, `next-session`, `jump-to-session`,
`previous-attention-session`, `next-attention-session`, `history-back`,
`history-forward`, `previous-tab`, `next-tab`, `new-shell-terminal`,
`split-shell-terminal`, `split-shell-terminal-down`, `close-shell-terminal`,
`reopen-closed-tab`, `archive-session`, `open-in-editor`,
`copy-session-path`, `model-picker`, `dictate`, `open-orchestrator`,
`toggle-sidebar`, `toggle-inspector`, `toggle-terminal`, `toggle-diff`, or
`toggle-browser-devtools`. A session switch made by a shortcut counts as
`switch_from_shortcut_count`.

### Definitions

- A rage click is the fourth pointer click on the same element within one
  second. A burst is counted once, however long it lasts. Double and triple
  clicks that select text, in a field or on text they leave selected, restart
  the count, so selecting words quickly is not a rage click.
- A dead click is a single pointer click inside a surface that lands on
  nothing interactive (no button, link, field, tab, menu item, terminal, named
  control or other focusable element), shows neither a pointer nor a text
  cursor, does not select text, and is not on a scrollbar. Double clicks are
  never dead, since they usually select text. A click that only closes an open
  menu, popover or list, or that the app handles and cancels, is not dead.
- Visible and focused time stop counting after 5 minutes with no input, and
  while the window is hidden or not focused. A surface that is mounted but
  hidden, such as a chat kept alive in the background, is not on screen.

### Limits and the opt-out

Counting happens in memory and is saved to the app's local storage every five
minutes and when the window closes. Turning off Share usage analytics drops
everything counted so far, removes the saved window and stops counting. The
daily view, rage-click and first-use markers are kept only so the same value
is not sent twice. The kill switch in `AO_TELEMETRY_DISABLED_EVENTS` silences
any of these events by name; silencing all five also stops the counting.
`ao.ui.error_shown` is sent at most once per 5 minutes for the same kind,
presentation, surface and code, and also uses the standard renderer caps of 5
per minute and 200 per day. The once-per-day and once-per-install events skip the per-minute cap,
since several can be sent together at launch, and keep the daily cap.

The listeners are passive and attached once to the document, so they cannot
delay a click, scroll or key press. Telling which surface or control was used
walks up the DOM from the event target and reads no layout. Each press looks
up open menus and popovers with one selector query, also without reading
layout. Hover is checked
at most once per animation frame, for the last element the mouse entered. The
only layout read is for a click on nothing interactive: the clicked element's
box and cursor, read after the click's frame has painted, to tell a dead click
from a scrollbar or a pointer-cursor element. App shortcuts are reported by
the main process as one small message per press, sent with the shortcut itself.

## UI responsiveness

When product telemetry is enabled, the desktop app sends launch timings in
`ao.launches.timed` and sends `ao.usage.ui_performance`, a numeric summary of
how fast the app responded. Their own properties are only counts, durations in
milliseconds, buckets, and the closed values listed here; like every event,
they also carry the shared context described above, such as `app_version` and
`install_id`. They never include a
session, project or file name, path, URL, message text, script URL, function
name, element id, text or attribute, or CSS selector, and the summary does not
say which session a measurement came from. The only names they carry are the
app's own: a browser entry point type such as `BUTTON.onclick`, and the name
of one of the app's code bundles such as `SessionView`.

| Event | When it is sent | Performance properties |
| --- | --- | --- |
| `ao.launches.timed` | Once per launch, when the window first shows the sidebar with live sessions, or when the window closes or 60 seconds pass without that frame (then without `to_usable_ms`). A reload sends none. `ao.app.loaded` is still sent at startup without these properties | `launch_kind`, `to_visible_ms`, `to_usable_ms`, and on a `first` or `cold` launch `to_app_ready_ms`, `to_window_shown_ms`, `to_server_spawn_ms`, `to_server_ready_ms`, `server_reused` |
| `ao.usage.ui_performance` | At the end of each window, 45 to 60 minutes of elapsed time (random in that range) that includes idle and hidden time, so a window can hold much less use than that. A window cut short by quitting is kept and sent at the next launch if its end has passed, otherwise when the rest of it runs out. Nothing is sent for a window with no use. Only installs with `experiment_cohort` below 50 send it | see below |

Properties on `ao.launches.timed`:

- `launch_kind` is `first` (a new app process that found no database, the
  first run on this data directory), `cold` (a new app process), `warm` (a
  window reopened while the app kept running), or `update` (the first cold
  launch on a new `app_version`).
- `to_visible_ms` is the time from the start of the launch to the window's
  first contentful paint. `to_usable_ms` is the time to the frame that first
  shows the sidebar with live sessions, the same frame the desktop log's
  "launch timing" line ends at. A cold launch starts when the app process
  starts; a warm one when the window is requested.
- A `first` or `cold` launch also sends the app's steps on the way, in ms
  from the app process starting: `to_app_ready_ms` (Electron ready),
  `to_window_shown_ms`, `to_server_spawn_ms` (the daemon process started) and
  `to_server_ready_ms` (the daemon's API answered). `server_reused` is true
  when the app attached to a daemon that was already running instead of
  starting one. A step that had not happened is left out.
- A launch whose window was hidden after it first appeared but before it
  became usable (the user minimized it or switched away) sends `launch_kind`
  only. Hiding it after it became usable keeps the timings. The window is
  created hidden and shown once the page loads; that wait is part of the
  launch.
- A window closed before it became usable sends the event as it closes,
  without `to_usable_ms` or the app's steps.

Properties on `ao.usage.ui_performance`:

- Window: `measurement_schema_version` (3), `flush_reason` (`interval` or
  `startup`), `window_seconds` (elapsed time while the app was running, so a
  window kept across a quit leaves out the time the app was closed; never less
  than `active_seconds`),
  `active_seconds` (time the window was visible
  with input in the last 5 minutes), `open_sessions_bucket` (`0`, `1-3`,
  `4-10`, `11-30`, or `31+` sessions that are not terminated), and
  `uptime_hours_bucket` (how long the window has been open: `lt1`, `lt8`,
  `lt24`, `lt72`, or `ge72` hours).
- Session switches: `switch_cached_lt150_count`, `switch_cached_lt500_count`,
  `switch_cached_lt1000_count`, `switch_cached_ge1000_count`, and the same four
  `switch_uncached_*` counts. Each switch from one session to another is timed
  from the click or key that asked for it (or from routing, for a menu
  shortcut) to the frame that shows the destination's messages, and counted in
  the bucket under 150, 500, or 1,000 ms, or 1,000 ms and over. Cached means
  the destination showed its content in its first frame: a chat whose messages
  were already in memory, or a terminal session. `switch_visible_p75_ms` is the
  75th percentile time to the first frame that shows the destination.
  `switch_dropped_count` counts switches not timed because the window was
  hidden during them or they took over 30 seconds.
- Clicks and keys: `interaction_count` (every click, tap and key press),
  `slow_interaction_count` (over 200 ms to the next frame),
  `very_slow_interaction_count` (over 500 ms), and `interaction_p98_ms`, the
  98th percentile computed the way Interaction to Next Paint is. These come
  from the browser's Event Timing API, which reports only interactions of
  40 ms or more, so `interaction_p98_ms` is 0 when none reached 40 ms: a
  fallback, not a measured duration.
- Frozen frames: `long_frame_count` (frames that took over 50 ms),
  `long_frame_blocking_ms` (their total blocking time), `long_frame_ms` (their
  total duration), `long_frame_max_ms` (the longest one), and
  `long_frame_no_script_count` (those the browser listed no script for). Each
  frame's time is split by phase into causes, and each cause's total is sent
  as `long_frame_<cause>_ms` with dashes as underscores, such as
  `long_frame_frame_callbacks_ms`: before rendering starts, each script the
  browser listed counts under its invoker type (`event-listener`,
  `user-callback` for timers and animation frames, `resolve-promise`,
  `reject-promise`, `classic-script`, `module-script`), less the style and
  layout it forced (`forced-layout`), and the rest of that time is `unlisted`;
  from the start of rendering to style and layout, listed scripts count the
  same way and the rest is `frame-callbacks` (animation frame and observer
  callbacks); style, layout and paint is `rendering`, less listed scripts that
  ran inside it (such as ResizeObserver callbacks), which count under their
  invoker type. The browser lists only
  scripts over 5 ms, so `unlisted` holds shorter scripts (React splits its work
  into slices of about 5 ms), garbage collection, parsing, other browser work,
  and gaps between tasks. `long_frame_top_cause` names the cause with the most
  time. `long_frame_top_invoker` is the entry point whose listed scripts took
  longest, reduced to its interface or HTML tag and member, such as
  `Window.requestAnimationFrame`, `MessagePort.onmessage` or `BUTTON.onclick`,
  with no ids, attributes or URLs; script loads are not named.
  `long_frame_top_bundle` is the app code bundle whose listed scripts took
  longest, such as `SessionView`, and is left out for code from anywhere else.
  Version 2 summaries put `unlisted` and `frame-callbacks` time under `other`,
  and version 1 also put rendering there.
- Chat open: `chat_open_count` and `chat_open_p75_ms`, for opening a chat whose
  messages were not in memory, from the click to the frame that shows them.
- Memory and CPU: `renderer_mb_bucket` (the window's peak memory) and
  `app_mb_bucket` (peak memory of all app processes), each `lt256`, `lt512`,
  `lt1024`, `lt2048`, or `ge2048` MB; `app_cpu_avg_pct`, the average CPU of all
  app processes since the previous summary, to one decimal place. The app
  samples these every 10 minutes. The daemon's own process is not included.

A missing count means zero; a missing percentile or cause means nothing
was measured for it. Measurements other than memory and CPU count only while
the window is visible; memory and CPU cover the whole time, so background use
is included. The summary window and its counts are kept in the app's local storage
between launches and replaced after each send. Share usage analytics and the
kill switch control both events. Turning Share usage analytics off stops the
summary and deletes the window counted so far, so nothing from before is sent
after it is turned back on.

## Mobile app screens and launches

When mobile telemetry is active, the phone records which of its screens and
sheets people use, the errors it shows them, how long it takes to start, and
how quickly it responds,
the mobile counterpart of "UI usage" and "UI responsiveness" above. The
properties specific to these events are fixed screen names, fixed error kinds,
counts and durations. Like every phone event they also carry the shared
context, such as `app_version`. They never include a session, project, PR or
file name, path, URL, message text, error message, tap position or
screenshot. There is no session replay.

| Event | When it is sent | Properties |
| --- | --- | --- |
| `mobile.surfaces.viewed` | A screen or sheet stays on screen for a second. At most once per surface per UTC day | `surface` |
| `mobile.ui.usage_summary` | When the app goes to the background at least 15 minutes after its window started, or at the next launch for a window that ran out while the app was closed. Not sent when nothing was counted | Window fields and counters (below) |
| `mobile.ui.error_shown` | A user-visible error shows: a warning toast, a board failure view, or a screen or sheet that failed to draw. The same error on the same screen within 5 minutes is counted in the summary but not sent again | `presentation`, `error_kind`, `surface` |
| `mobile.usage.ui_performance` | With `mobile.ui.usage_summary`, at the end of the same window. Not sent when nothing was measured | `measurement_schema_version`, `window_seconds`, `active_seconds`, `flush_reason`, and the measurements below |
| `mobile.launches.timed` | Once per app start: when a paired machine's live data or Orchestrator Cloud's first list shows, when 60 seconds pass without it, or when the app goes to the background first | `launch_kind`, `outcome`, `landing_surface`, `clock_origin`, `to_js_start_ms`, `to_visible_ms`, `to_usable_ms` |

- `surface` is one of the app's screens or sheets: `board`, `projects`,
  `project`, `compose`, `spawn`, `prs`, `notifications`, `session` (the session
  screen while it loads), `session_chat`, `session_tui`, `session_cloud`,
  `conversation_actions`, `review`, `review_actions`, `reviewer`, `files`,
  `file_viewer`, `files_review`, `files_branch`, `files_changes`, `artifacts`,
  `artifact`, `artifact_history`, `render`, `preview`, `browser`, `device`,
  `shell`, `cloud_terminal`, `cloud_new_project`, `cloud_plan`,
  `conversation_usage`, `model_picker`, `agent_picker`, `account`,
  `machine_name`, `onboarding`, `pair`, `connect`, `settings`,
  `settings_theme`, `settings_machine`, `settings_cloud`, or `other` for a
  screen added later.
- `presentation` is `toast`, `screen`, or `sheet`. `error_kind` is
  `render_crash`, `unreachable`, `auth`, `incompatible_host`, `rate_limited`,
  `server_error`, `phone_offline`, `cloud_unavailable`, or `action_failed` (a
  warning toast after an action).

`mobile.ui.usage_summary` properties (a counter that stayed at 0 is left out):

| Property | Meaning |
| --- | --- |
| `measurement_schema_version` | `1` |
| `window_seconds` | Wall time from the window's start to the send, or, for a window sent at the next launch, to its last save while the app was open (time the app was closed is not counted) |
| `active_seconds` | Time the app was in the foreground |
| `flush_reason` | `background`, or `startup` for a window that ended while the app was closed |
| `foreground_count` | Launches and returns to the foreground |
| `error_shown_count` | User-visible errors, including repeats not sent as their own event |
| `session_open_count` | Sessions opened, each once while it stays open: a remount or a chat/terminal switch is not a new opening, going straight to another session is |
| `session_open_from_<source>_count` | Those by where they were opened from: the screen before it (`board`, `project`, `prs`, `spawn`, `notifications_inbox`), another session (`session`), a push tap that opened it (`notification`), a link (`deep_link`), the launch reopening the session the app was left on (`restore`), or `other` |
| `surface_<surface>_seconds` | Time that screen was on screen while the app was in the foreground |
| `surface_<surface>_opens` | Times it came on screen, including returning to it |

Properties on `mobile.usage.ui_performance` (`measurement_schema_version` is
`1`; a count that stayed at 0, or a percentile with nothing measured, is left
out):

- Session opens: `session_open_count`, counted in
  `session_open_lt500_count`, `session_open_lt1000_count`,
  `session_open_lt3000_count` and `session_open_ge3000_count`, and
  `session_open_p75_ms`: from the session screen coming up after another
  screen to the session showing its content, that is the chat's messages
  drawn or the terminal's first output (for an Orchestrator Cloud session, its
  conversation or its agent's terminal output). A return to a session already
  open, or a switch between sessions, is not timed.
  `session_open_dropped_count` counts openings not timed because the screen
  was left, another session was opened, or the app went to the background
  before the content showed, or because it took over 30 seconds, as the
  desktop's `switch_dropped_count` does.
- Main-thread stalls: a timer asked to run every 500 ms while the app is in
  front and telemetry is on reports how late it ran. `stall_count` counts runs 100 ms or more late,
  `stall_long_count` those 500 ms or more, `stall_total_ms` their lateness and
  `stall_max_ms` the worst. A run 10 seconds or more late is the app having been
  suspended and is not counted.
- Requests to paired machines, timed to the answer's headers:
  `request_count` (requests that got an answer), `request_lt300_count`,
  `request_lt1000_count`, `request_lt3000_count`, `request_ge3000_count`,
  `request_p75_ms` and `request_p95_ms`. `request_error_count` counts the
  answers that were an error status; they are included in `request_count`.
  `request_unanswered_count` counts requests that timed out or got no answer;
  they are not included in `request_count`. By connection kind,
  `request_<kind>_count` and `request_<kind>_p75_ms`, where `<kind>` is `lan`,
  `tailscale`, `tunnel`, `relay`, or `unknown`. A request the app cancelled
  because the screen was left, or one still in flight when the app went to the
  background, is not counted. Orchestrator Cloud requests are not included.
- Returns to the app from the background with a machine paired:
  `resume_count`, and the time until a paired machine's data was fresh again
  in `resume_fresh_lt1000_count`,
  `resume_fresh_lt3000_count`, `resume_fresh_ge3000_count` and
  `resume_fresh_p75_ms`. `resume_stale_count` counts returns that went back to
  the background with no fresh data, or got it after 30 seconds or more.
  Coming back from iOS's `inactive` state (Control Center, Notification
  Center, the app switcher, Face ID or a system alert) is not a return: the
  app never left and kept refreshing.

The percentiles come from the last 100 measurements of each kind in the window;
the counts include every measurement.

Properties on `mobile.launches.timed`:

- `launch_kind` is `first` (the first launch of an install with this
  telemetry), `update` (the first launch on a new app version or OTA update), or
  `cold`.
- `outcome` is `usable`, `timeout`, `no_machine` (the 60 seconds passed on a
  phone with no paired machine and no Orchestrator Cloud, which has nothing to
  load), or `backgrounded`. A launch that went to the
  background before it was usable sends only `launch_kind`, `outcome` and
  `landing_surface`.
- `landing_surface` is the screen on show when the launch ended, such as
  `board`, or `onboarding` on a phone that is not paired yet.
- Times are in milliseconds from the app's start, read from React Native's
  startup marks; `clock_origin` is `process`, or `runtime` when the app's own
  start was not recorded and the JavaScript runtime's start is used instead.
  The app does not record its own start today, so launches report `runtime`.
  `to_js_start_ms` is the app's JavaScript starting (sent only with `process`:
  measured from the runtime's start it is always 0), `to_visible_ms` its first
  frame, and `to_usable_ms` the first frame after a paired machine's data or
  Cloud's first list arrives. A time outside 0 to 10 minutes is left out.

Counting happens in memory and is saved in the app's storage when it goes to
the background, and at most once a minute while screens change. The phone's
analytics opt-out (which follows the paired desktop's) drops everything
counted so far, removes the saved window, and stops counting; nothing counted
before an opt-out is sent after opting in again. Development builds do not
count. The once-per-day screen markers are kept only so the same screen is not
sent twice in a day. A screen view or shown error that was not sent (rate
limit, kill switch, or the preference still loading) is tried again on its next
showing.

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

## Desktop onboarding funnel

When desktop product telemetry is enabled, Orchestrator.inc records how new
users move through first-run onboarding, so we can see where they stop and
why. The flow is: the startup dependency dialog (only when git, tmux or a
coding agent is missing, before onboarding), the welcome page and its three-part story (the last
part shows CI and code review on GitHub), the optional AO Cloud sign-in, Your
tools (the GitHub CLI and coding agents), then a guided tour of the real app
(add a project, open its orchestrator, send the first message).

The onboarding properties below are only the closed values listed here,
counts, buckets and durations: never a path, repository name or URL, project
or session name, username, email, chat text or error message. Like every
renderer event, these events also carry the shared context and identity
properties documented later in this file, including `github_actor` when the
GitHub CLI is signed in and `ao_cloud_user_id` when signed in to AO Cloud.

### Steps

The funnel to read is the pages and then the tour: `welcome` → `sign_in` →
`tools` → `tour_project` → `tour_orchestrator` → `first_task` → `finish`.
Three steps are conditional sub-steps, not stages every run passes through,
so `step_index` is an order, not a dense funnel:

- `dependencies` shows only when git, tmux or a coding agent is missing, and
  before `started`. It counts only while onboarding has not been seen yet; the
  same dialog after onboarding (a tool removed months later) sends nothing.
- `github_auth` shows inside Your tools, or in the clone dialog opened from
  the tour, only while the GitHub CLI needs installing or signing in. It has
  no `already_done` skip when Your tools itself is skipped. The clone dialog
  outside onboarding sends nothing.
- `import_chats` is not reachable today.

Every step is identified by `step` and its position in the flow,
`step_index`:

| `step_index` | `step` | What it is |
| --- | --- | --- |
| 0 | `dependencies` | The startup dialog for missing git, tmux or a coding agent |
| 1 | `welcome` | The first page and its story |
| 2 | `sign_in` | The optional AO Cloud account page |
| 3 | `tools` | Your tools: the GitHub CLI and coding agents |
| 4 | `github_auth` | Signing the GitHub CLI in, on Your tools |
| 5 | `import_chats` | Importing earlier chats. Not shown since the first-run prompt on Home was removed; recorded if it returns |
| 6 | `tour_project` | Tour: open or add a project |
| 7 | `tour_orchestrator` | Tour: open the project's orchestrator |
| 8 | `first_task` | Tour: send the first message |
| 9 | `finish` | Tour: the closing note |

| Event | When it is sent | Onboarding-specific properties |
| --- | --- | --- |
| `ao.onboarding.step_viewed` | A step is shown | `step`, `step_index`, `run_kind`, `missing` (dependencies, tools), `agents_ready` (tools), `chats_found_bucket` (import_chats) |
| `ao.onboarding.step_completed` | A step is done: Continue on a page, the tour moving on, the tool signed in | `step`, `step_index`, `run_kind`, `time_on_step_ms`; `story_points_seen` and `story_completed` (welcome); `agents_ready` and `gh_ready` (tools); `project_source` (tour_project) |
| `ao.onboarding.step_skipped` | A step is passed over: automatically because there was nothing to do, by the person (Sign in's "do it later", Skip setup, Skip on the import step), or by closing the tour | `step`, `step_index`, `run_kind`, `reason`, `time_on_step_ms` |
| `ao.onboarding.step_abandoned` | The window closes while the step is showing. Sent at once with `sendBeacon` | `step`, `step_index`, `run_kind`, `time_on_step_ms` |
| `ao.onboarding.back_clicked` | Back is clicked | `step`, `step_index`, `to_step`, `run_kind` |

`step_viewed`, `step_completed`, `step_abandoned` and an automatic
`step_skipped` are each sent at most once per installation (once per replay
for a replay), so the counts read as a funnel. A person's skip and Back are
sent on each click. `time_on_step_ms` runs from the step's latest showing in
this launch. `step_index` is derived from `step` by the app, never taken from
the page that sent the event. Your tools sends its view once its checks have
answered (at most 3 seconds), so `missing` and `agents_ready` are complete;
leaving it or closing the window sooner still sends the view first, with what
was known. Its `time_on_step_ms` runs from when it opened.

### The run

| Event | When it is sent | Onboarding-specific properties |
| --- | --- | --- |
| `ao.onboarding.started` | Onboarding opens for the first time on this installation, or a replay starts | `step`, `step_index`, `run_kind` |
| `ao.onboarding.resumed` | Onboarding opens on a later launch after the app was quit mid-flow | `step`, `step_index`, `run_kind`, `away_ms`, `away_bucket`, `launch_count` |
| `ao.onboarding.abandoned` | The window closes mid-flow. At most once per launch. Sent at once with `sendBeacon` | `step` (the page showing), `step_index`, `run_kind`, `duration_ms`, `duration_bucket`, `launch_count` |
| `ao.onboarding.completed` | Onboarding ends. Once per run | `step` (the last page shown: `welcome`, `sign_in` or `tools`), `step_index`, `run_kind`, `outcome`, `duration_ms`, `duration_bucket`, `launch_count`, `resumed`, `back_count`, `auto_skipped_count` |
| `ao.onboarding.activated` | The first chat message delivered after a first run finished, within 7 days. Once per installation | `milestone`, `time_since_completed_ms`, `time_bucket`, `during_tour` |

PostHog sends its queue from its own `pagehide` handler before the app's run,
so `abandoned` and `step_abandoned` bypass the queue and go out with
`sendBeacon`, which outlives the page. A quit that still loses them is
visible: the run has `started` without `completed`, and a later launch sends
`resumed`.

### Actions

Installs and sign-ins started during onboarding report how each attempt
ended. The tour's project step sends no action: its add or clone is already
`ao.projects.added`, `ao.projects.add_failed` or `ao.projects.clone_failed`,
and `project_source` on its `step_completed` says which it was.

| Event | When it is sent | Onboarding-specific properties |
| --- | --- | --- |
| `ao.onboarding.action_started` | An install or sign-in starts, by a click or (`auto`) by the page, as the GitHub CLI login does once | `action`, `target`, `step`, `step_index`, `attempt`, `auto` |
| `ao.onboarding.action_finished` | That attempt ends, once, or onboarding ends while it still runs (`left`) | `action`, `target`, `step`, `step_index`, `attempt`, `outcome`, `reason`, `error_code`, `duration_ms` |

`attempt` counts from 1 for each action and target in a launch, so a value
above 1 is a retry. The existing `ao.projects.clone_failed`,
`ao.projects.add_failed` and `ao.projects.added` events are unchanged.

Some events fire together for one thing that happened; count one of each pair,
not both:

- `step_completed` (`github_auth`) and `action_finished` (`gh_sign_in`,
  `succeeded`) when the GitHub CLI signs in.
- `action_finished` (`cloud_sign_in`, `succeeded`) and
  `ao.renderer.cloud_signed_in` with `source` `onboarding`.
- `activated` with `during_tour` true and `step_completed` (`first_task`).

### Onboarding errors in Sentry

Onboarding errors go to the desktop Sentry project under **Share error
events**, not the analytics choice, so they are sent with analytics off and
stop when error sharing is off. A failed install or sign-in is sent from every
install, not just the one in ten in the error sample (`sample_rate` `1`); the
daily caps above still apply. The flow tag on other errors follows the usual
sampling.

- An install or sign-in started during onboarding that breaks is one Sentry
  issue per attempt, titled `Onboarding <action> failed (<reason>)` and grouped
  by action and reason alone. Its tags are `category` `onboarding`, `code`
  `<action>:<reason>`, `action`, `reason`, `target` and `flow` `onboarding`.
  `target` is what the action was for, from a fixed list in
  `shared/onboarding-failures.ts` (`gh`, `tmux`, `claude-code`, `codex` and
  the other registry agents), or `other` for any id not on it. It is a tag to
  filter by, not part of the grouping. Only these
  reasons are sent: `start_failed`, `install_failed`, `interrupted`,
  `lost_contact`, `cleanup_failed`, `request_failed` and `other`. A login the
  person did not finish (`not_signed_in`), a platform with no installer
  (`unsupported`), an agent that signs in outside the app (`documentation`),
  cancelled and timed-out attempts are setup states, counted only in PostHog.
  The same panels outside onboarding (the clone dialog's GitHub sign-in) send
  nothing, and an attempt stops counting once onboarding ends (outcome
  `left`), so a later failure is not filed as an onboarding issue. The API
  error code stays in PostHog's `action_finished`.
- Every other renderer Sentry event (a render crash, a failed query, an API
  error) sent while the first-run window shows, or a tour step in the launch
  whose onboarding pages finished, carries `flow:onboarding`. A tour restored
  on a later launch stays until it is dismissed, so it does not tag those
  launches' errors. The tag does not change how the event is grouped.
  Electron main's own events (process crashes, handled update or IPC
  failures) do not carry it: main does not track which screen the renderer
  shows.
- To keep this working while analytics is off, the app holds the actions
  started during onboarding, with an id for each attempt, in memory until each
  ends. The id lets a check that outlived its attempt be ignored. Nothing about
  them is stored or sent to PostHog.

### Property values

- `run_kind` is `first_run` or `replay` (Replay onboarding in Developer Mode,
  in development builds only, so published builds send `first_run`). A replay
  is a separate run with its own step counts and is never counted as
  activation.
- `missing` is a comma-separated list of `git`, `tmux`, `gh` (GitHub CLI not
  installed), `gh_auth` (installed, signed out), `agent` (no coding agent
  installed) and `agent_auth` (installed, none signed in).
- `agents_ready` is the number of coding agents ready to use; `gh_ready` is
  whether the GitHub CLI is installed and signed in.
- `chats_found_bucket` is `0`, `1`, `2_5`, `6_20` or `21_plus`. The folders
  picked are counted by `ao.session_imports.completed` (`source` `onboarding`).
- `story_points_seen` is how many of the welcome story's three parts played
  before Get started (1 to 3); `story_completed` is true once the whole story
  played at least once, CI part included.
- `project_source` is `clone`, `local_folder`, `existing` (a project that was
  already there) or `other` (such as an AO Cloud project).
- `reason` on `step_skipped` is `already_done`, `user` or `dismissed`.
- `outcome` on `completed` is `finished` (the last page's Continue),
  `skipped_setup` (Skip setup on Your tools) or `superseded` (a later launch
  found a project, or an offer to bring over data from an older installation,
  and closed onboarding for the person).
- `duration_bucket` and `away_bucket` are `lt_1m`, `1m_3m`, `3m_10m`,
  `10m_30m`, `30m_2h`, `2h_1d` or `gte_1d`. `launch_count` is how many launches
  the run has spanned. Durations are capped at 30 days.
- `time_bucket` on `activated` is `lt_5m`, `5m_15m`, `15m_1h`, `1h_1d` or
  `1d_7d`. `milestone` is `first_message`. `during_tour` is true when the
  message was the tour's task step.
- `action` is `dependency_install`, `gh_install`, `gh_sign_in`,
  `agent_install`, `agent_sign_in` or `cloud_sign_in`. `target` is the install target or agent id from AO's own
  registry, such as `gh`, `tmux` or `claude-code`.
- `outcome` on `action_finished` is `succeeded`, `failed`, `cancelled` (the
  person closed the login or moved on), `timed_out` (a login left open 15
  minutes), `handed_off` (the agent signs in outside AO, so its guide
  opened) or `left` (onboarding ended, or the window closed, while it was
  still running: Skip setup, onboarding closed for the person, the tour ending
  or closing, or quitting mid-onboarding). An install or sign-in from the
  onboarding pages keeps running into the tour, so its `left` comes when the
  tour ends and its `duration_ms` includes the tour.
- `reason` on `action_finished` is `start_failed`, `install_failed`,
  `interrupted`, `unsupported`, `lost_contact`, `not_signed_in`,
  `cleanup_failed`, `request_failed`, `documentation` or `other`.
  `error_code` is the API's error code, never its message.

`ao.renderer.cloud_signed_in` also carries `source` `onboarding` when the
sign-in finished within 30 minutes of clicking Sign in on onboarding's account
page. That note is kept in memory only.

The renderer's usual limit of five events per name per minute applies per
step and action for these events, so clicking quickly through onboarding does
not drop later steps. A sixth quick retry of the same action within a minute
drops both its `action_started` and its `action_finished`; it shows only as a
gap in `attempt`.

They stop when Share usage analytics is off, and a kill-switch entry in
`AO_TELEMETRY_DISABLED_EVENTS` silences them like any other event. While
analytics is off nothing is counted or stored for them either, and turning it
off drops the run in progress, attempt counts and step timings, so an event
sent after turning it back on carries nothing from the time it was off. A run
that was off when it began has no `started`, and so sends no `completed`.

`ao.onboarding.first_run_experiment_exposed` and
`ao.onboarding.first_run_prompt_action` are no longer sent and their
properties are no longer allowed: they belonged to the first-run prompt on
Home, which has been removed. Builds from before that still send them.

## What Orchestrator.inc does not intentionally send

Product telemetry is designed not to include:

- Source code, diffs, commits, or file contents
- Prompts, agent conversations, agent output, or terminal contents
- Shell command arguments, command history, or environment variables
- Repository names, project names, branch names, or plain-text file paths
- API keys, access tokens, passwords, or other credentials
- Names, or email addresses other than the one on your signed-in AO Cloud
  account (invitee emails are never sent)
- Screen recordings, session replays, screenshots, pointer coordinates, or
  the text of anything you click or hover

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
  `GET /api/v1/sessions/:id/conversation/models`,
  `GET /api/v1/sessions/:id/conversation/skills`, and
  `GET /api/v1/agents/:id/skills`; none of these 4xx responses go to Sentry,
  so a remote host too old to scan skills for the new chat page stays quiet. Codex
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
reportable renderer errors, and installs and sign-ins that broke during
onboarding (see "Onboarding errors in Sentry"). Daemon events use fixed failure
or panic labels and allowlisted error codes. The daemon groups HTTP faults by
registered route and stable error
code, or by a bounded cause type when no code exists. Ordinary Go errors carry
no synthetic throw stack. Recovered panics retain the interrupted AO function.
Agent relaunches group by operation and failure step, with a fixed step tag
and no HTTP route tag.
An event may include a bounded `git`, `db`, `agent`, or `network` area when the
error provides evidence. Panic events keep function and line information, but
remove stack paths, request data, project, session, repository and branch
names, and panic text. Electron issues use application codes, eligible engine
messages, or a computed title based on the type, location, and category.
Uncoded messages group by a hash of the locally normalized text.
Daemon and Electron events use the same installation ID as Sentry `user.id`.
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
| `ao.v2.mobile_app.onboarding_skipped` | `mobile.app.onboarding_skipped` (retired: onboarding has no skip, so it is not sent) |
| `ao.v2.mobile_app.feature_used` | `mobile.app.feature_used` |
| `ao.v2.mobile_app.notification_opened` | `mobile.app.notification_opened` |
| `ao.usage.*` (`worker_pr_associated`, `worker_pr_merged`, `messages_sent`) | unchanged |
| `ao.usage.worker_pr_closed` | not shipped under this name; the shipped event `ao.prs.closed` is unchanged |
| `ao.usage.daily_summary` | not shipped; nothing to rename |
| `ao.http.5xx` | unchanged |
| (new) | `ao.setup.checked` |
| `ao.renderer.artifact_create_succeeded` | `ao.artifacts.session_started` |
| `ao.renderer.artifact_create_failed` | `ao.artifacts.session_start_failed` |
| (new) | other `ao.artifacts.*` events (see "Artifacts") |
| (new) | `ao.surfaces.viewed`, `ao.features.first_used`, `ao.ui.usage_summary`, `ao.ui.error_shown`, `ao.ui.rage_clicked` (see "UI usage") |


## Questions or corrections

For the broader data policy, retention information, and contact options, see
the [Orchestrator.inc privacy policy](https://orchestrator.inc/privacy). You can report a problem
with this documentation in the
[GitHub repository](https://github.com/OrchestratorInc/agent-orchestrator).

Handled Electron main captures are limited per sanitized category, reason and detail to five per minute and 200 per UTC day, with at most 1,024 budget keys per day. Pending pre-policy captures count against the same limits. Renderer operation tags are forwarded only for fixed exception operations. Remote proxy connectivity and identity-verification refusals are expected states.
