# PostHog Cost Controls

This page is the runbook for cutting AO PostHog spend while preserving active
usage and reliability observability.

## Current finding

Read-only HogQL on 2026-07-29 against PostHog project `475752` found
5,598,610 events in the trailing 30 days and 2,435,110 events in the trailing
7 days. The volume is concentrated in legacy CLI telemetry:

| Window | `ao.cli.invoked` | `ao.app.active` | `ao.renderer.route_viewed` |
| --- | ---: | ---: | ---: |
| 30 days | 2,667,927 | 2,553,297 | 150,586 |
| 7 days | 1,167,224 | 1,151,190 | 45,710 |

In the trailing 7-day window, legacy `ao hooks` alone produced 962,837
`ao.cli.invoked` events and 947,731 CLI-channel `ao.app.active` events. Those
events had `actor_type = null` and no `$process_person_profile = false`, which
identifies them as old uncapped clients. Current builds emit bounded, anonymous
telemetry, but old installs cannot be forced to upgrade.

Three client generations share the project. Tell them apart by name and by
`telemetry_schema_version`:

| Generation | Names | `telemetry_schema_version` |
| --- | --- | --- |
| Legacy, uncapped | `ao.cli.invoked`, `ao.app.active` (CLI firehose above) | absent |
| Previous release | `ao.v2.app.active`, `ao.v2.cli.invoked`, `ao.v2.renderer.*`, `ao.v2.mobile_app.*`; `legacy_event_name` on renamed daemon events | `2` |
| Current release | `{product}.{object}.{past_tense_verb}` names (full table in [Product telemetry](telemetry.md#event-names)), no `legacy_event_name` | `3` |

The current release exports `ao.cli.invoked` and `ao.app.active` again, the same
names as the legacy firehose. Current traffic under those names is capped and
carries `telemetry_schema_version = 3`; legacy traffic has no version. Every
dashboard or rule that reads those two names must filter on
`telemetry_schema_version` to separate them. CLI activity is `ao.cli.active` and
phone activity is `mobile.app.active`, so `ao.app.active` from a current client
is always the desktop.

## Ingestion Rules

Configure PostHog ingestion controls for project `475752` in this order.

**Release gate:** the live transformation must be updated to these rules, and
any event-name allowlist must include the current names, *before* the release
with `telemetry_schema_version = 3` ships. Rules written only against the
`ao.v2.*` and `ao.session.*` names would drop or miss the renamed events.

Define `normalized_command_path` as `command_path` lowercased, trimmed, and with
repeated whitespace collapsed. A routine command is one where
`normalized_command_path` equals one of these paths, or starts with one of them
followed by a space:

- `ao hooks`
- `ao session ls`
- `ao session get`
- `ao orchestrator ls`
- `ao status`
- `ao project ls`
- `ao project get`
- `ao pty-host`

1. Keep events with `telemetry_schema_version` 2 or 3 unless a rule below drops
   them (`ao.v2.*` from the previous release, the renamed events from the
   current one).
2. Drop `ao.cli.invoked` when the command is routine, regardless of
   `actor_type`.
3. Drop `ao.app.active` when `channel = 'cli'` and the command is routine,
   regardless of `actor_type`.
4. Keep legacy `ao.app.active` where `channel = 'renderer'` so old desktop-only
   installs still contribute to DAU until they update.
5. Keep low-volume reliability events: `ao.sessions.spawned`,
   `ao.sessions.spawn_failed`, `ao.sessions.spawn_cancelled`,
   `ao.session.relaunch_failed`,
   `ao.sessions.input_wait_started`, `ao.sessions.input_wait_ended`,
   `ao.http.5xx`, `ao.daemon.panic`, `ao.requests.failed`, `ao.daemon.failed`,
   and the harness-update rollout events `ao.harnesses.updated`,
   `ao.harness.update_idle_wait` and `ao.harnesses.version_switched`. Keep the
   previous names (`ao.session.spawned`, `ao.session.spawn_failed`,
   `ao.session.waiting_input_entered`, `ao.session.waiting_input_exited`,
   `ao.harness.update`, `ao.harness.version_switch`) for installs that have not
   updated.
6. Keep `ao.sessions.ended` (one per session end). The `ao.usage.*` summaries
   stay batched for messages; new worker PR outcomes are one event per PR
   (`count: 1`), which is low volume next to messages.
7. Drop `$web_vitals` unless a time-boxed performance investigation needs it.
8. Apply the same routine-command drop as a defensive backstop to
   `ao.cli.invoked` and `ao.cli.active` with `telemetry_schema_version = 3`, and
   to `ao.v2.cli.invoked` and CLI-channel `ao.v2.app.active`, even though
   current clients suppress those routine events before transmission.

`actor_type` is still useful for segmentation and analysis, but it must not be
part of the ingestion cost-control predicate. The command describes the activity
to exclude, while `actor_type` changed across client generations.

Examples the ingestion rule should cover:

| Event | Command path | Actor | Result |
| --- | --- | --- | --- |
| `ao.cli.invoked` | `ao hooks` | `agent` | Drop |
| `ao.cli.invoked` | `AO  HOOKS` | `user` | Drop |
| `ao.cli.invoked` | `ao hooks claude-code post-tool-use` | `user` | Drop |
| `ao.app.active` (`channel = cli`) | `ao session get sess-123` | `user` | Drop |
| `ao.cli.invoked` | `ao spawn` | `user` | Keep |
| `ao.app.active` (`channel = renderer`) | n/a | `renderer` | Keep |

When these project-side ingestion controls are enabled, the 7-day estimate is a
reduction from roughly 2.4M total events to well under 250k, before organic
adoption of current builds. That is a 10x+ reduction while keeping renderer
DAU, current CLI DAU, current command adoption, and reliability events.
The app code alone does not enforce these PostHog UI rules for already-deployed
legacy clients.

Deployment boundary checklist:

- Merging this PR protects clients that receive the next release.
- Update the live transformation to the renamed events before that release
  ships (see the release gate above).
- Updating this Markdown does not modify the live PostHog project.
- Update the live PostHog transformation separately with the same
  actor-independent routine-command rule to stop already-deployed clients.
- Verify the live transformation with the examples above, then check volume
  shortly after enabling it and again after 24 hours.

## Follow-up: Failure-only Internal CLI Telemetry

Successful background polling commands are not useful enough to justify
billable PostHog volume. Do not track routine successful executions for
internal/read-only commands such as:

- `ao status`
- `ao session ls`
- `ao session get`
- `ao project ls`
- `ao project get`
- `ao orchestrator ls`
- `ao hooks`
- `ao pty-host`

Keep meaningful failures, because they are reliability signal. A future
failure-only event should use a separate name such as `ao.cli.failed`
instead of reusing `ao.cli.invoked`.

Safe properties:

- `command_path`, for example `ao session ls`
- `actor_type`, for example `renderer`, `user`, `agent`, or `system`
- `error_category`, for example `daemon_unavailable`, `timeout`, or
  `backend_5xx`
- `error_code`, when it is a stable code such as `CONNECTION_REFUSED`
- `app_version` / `ao_version`
- `telemetry_schema_version`

Do not send raw error messages, stack traces, local paths, project names,
repository URLs, prompts, terminal output, access tokens, request payloads, or
other user content.

Do not treat expected outcomes as serious telemetry failures: user-cancelled
operations, dialogs closed by the user, already-removed projects, transient
polling failures while AO is starting, intentionally deleted resources, and
commands that succeed after automatic retry.

Repeated failures from polling should be deduplicated. Emit the same
`ao.cli.failed` shape at most once per install and time window, then include
`occurrence_count`, `window_start`, and `window_end` so 48 identical failures
cost one event while still showing the true magnitude.

The rule of thumb is: drop successful background polling events, but preserve
meaningful user-impacting failures as safe, rate-limited error telemetry.

## Abuse Controls

The PostHog project token is public in shipped desktop apps. Treat it like a
write-only routing key, not as an abuse boundary: an attacker can call
PostHog's capture endpoint directly with that token and bypass every
client-side or daemon-side limiter in AO.

Use layered controls:

1. Set PostHog billing limits for Product Analytics, Error Tracking, and
   Session Replay. This is the hard stop that prevents a surprise bill if a
   token is abused or a new event loops unexpectedly.
2. Keep the ingestion drop rules above enabled. They block the known legacy
   firehose before events are stored or billed.
3. Add a PostHog transformation for emergency abuse filtering. The
   transformation should return `null` for obviously invalid payloads, unknown
   event families, or event names outside AO's allowlist. Dropped events are
   unrecoverable, so do not use this for normal sampling of DAU events. If an
   allowlist is live, it must accept the current families (`ao.sessions.*`,
   `ao.projects.*`, `ao.tasks.*`, `ao.orchestrators.*`, `ao.reviews.*`,
   `ao.harnesses.*`, `ao.requests.failed`, `ao.daemon.failed`, `ao.app.*`,
   `ao.cli.*`, `ao.routes.viewed`, `ao.usage.*`, `ao.setup.checked`,
   `mobile.app.*`, and the rest of the table in
   [Product telemetry](telemetry.md#event-names)) before the release ships.
4. Keep current-client caps in AO:
   - renderer captures are bounded per event name per minute and per day
   - daemon remote exports are bounded per event name per minute and per day
   - burst-prone daemon failures are aggregated before export
   - `ao.usage.*` summaries skip the minute cap and use a 100,000 per-name
     daily emergency cap, rather than the standard 200 per-name cap
5. Use the AO kill switch for a stream that turns out to be noisy. Setting
   `AO_TELEMETRY_DISABLED_EVENTS` silences named streams (with `*` prefix
   matching) on installs that already exist, without shipping a release. Local
   SQLite still records them, so the stream stays debuggable. See
   [AO event kill switch](#ao-event-kill-switch) below. This is the client-side
   counterpart to a PostHog ingestion rule: the rule stops paying for events
   already sent, the switch stops sending them.

Those steps protect cost from normal bugs and known old clients, but they do
not fully protect a public project token from deliberate abuse.

### AO event kill switch

`AO_TELEMETRY_DISABLED_EVENTS` is a comma-separated list of event streams that
must not reach PostHog from the desktop or daemon:

```bash
AO_TELEMETRY_DISABLED_EVENTS="ao.app.active, ao.renderer.*"
```

Whitespace around entries is ignored. An entry ending in `*` after a non-empty
prefix matches by prefix. Both exact and prefix matching are case-insensitive
and check the event name and every name it was exported under before the
rename (for example `ao.v2.app.active` or `ao.session.*`), so an entry written
against an old name keeps silencing the renamed stream.

The desktop supervisor passes the list to both remote producers: the daemon's
PostHog sink and the renderer, which sends directly to PostHog. A stream is
therefore silenced across both paths after AO restarts. This is a per-install
launch setting, not a remotely managed switch: the variable must reach the AO
desktop process itself. A shell startup file may not be inherited when AO is
launched from the macOS Finder or Dock.

Mobile telemetry is not affected by this variable. Mobile builds instead use
`EXPO_PUBLIC_AO_TELEMETRY_DISABLED=1` to disable all telemetry or
`EXPO_PUBLIC_AO_TELEMETRY_DISABLED_EVENTS` for a comma-separated event list.
Those values are compiled into the app, so an ingestion drop rule is the only
way to silence a mobile stream in builds already shipped.

The switch is applied before aggregation, rate limiting, and export, so a
disabled stream consumes none of those resources. Local SQLite storage is
deliberately unaffected and continues to keep the raw operational event for
local debugging. Empty entries, a bare `*`, and unrecognized event names are
inert rather than preventing AO from starting.

The stronger standard pattern is to send telemetry through an AO-owned
collection proxy instead of sending directly to PostHog:

1. Ship future apps with `VITE_AO_POSTHOG_HOST` and
   `AO_TELEMETRY_POSTHOG_HOST` pointing at an AO telemetry collector, not
   directly at `https://us.i.posthog.com`.
2. Put edge rate limits in front of the collector:
   - per source IP
   - per install ID / `distinct_id`
   - per event name
   - per request body size
3. Validate the event allowlist and required properties at the collector.
4. Drop or sample low-value diagnostic events at the collector before they
   reach PostHog.
5. Forward accepted events to PostHog with the real project token stored only
   in collector configuration.
6. Rotate the PostHog project token after the collector path is live. Keep
   old-token ingestion rules restrictive so old apps can still contribute
   renderer DAU where needed, but cannot burn spend through CLI automation.

Do not rely on IP limiting alone. Many real users can share one NAT or VPN IP,
and one attacker can rotate IPs. IP limits are useful as an edge backstop, but
the primary product-specific limits should be per install ID, per event name,
and per time window.

## Dashboard Migration

Active users are a union of people, not a sum of events: someone active on the
desktop and the CLI on the same day counts once. Count distinct persons across
the active events of every generation:

```sql
SELECT
    toDate(timestamp) AS day,
    uniqExact(person_id) AS active_users
FROM events
WHERE timestamp >= now() - INTERVAL 30 DAY
  AND (
    -- Current release.
    (event IN ('ao.app.active', 'ao.cli.active', 'mobile.app.active')
      AND properties.telemetry_schema_version = 3)
    -- Previous release.
    OR event = 'ao.v2.app.active'
    -- Legacy clients: desktop, plus CLI activity a person started.
    OR (event = 'ao.app.active' AND properties.telemetry_schema_version IS NULL
      AND (properties.channel = 'renderer'
        OR (properties.channel = 'cli' AND properties.actor_type = 'user')))
  )
GROUP BY day
ORDER BY day
```

Use `uniqExact(properties.install_id)` instead of `person_id` for active
installations.

For historical DAU before v2 rollout, keep existing `ao.app.active` charts but
filter out legacy CLI automation:

```sql
SELECT
    day,
    uniqExact(distinct_id) AS active_installs
FROM (
    SELECT
        distinct_id,
        toDate(timestamp) AS day,
        lower(trim(replaceRegexpAll(toString(properties.command_path), '[[:space:]]+', ' '))) AS normalized_command_path,
        properties.channel AS channel
    FROM events
    WHERE timestamp >= now() - INTERVAL 90 DAY
      AND event = 'ao.app.active'
)
WHERE NOT (
    channel = 'cli'
    AND (
        normalized_command_path IN (
            'ao hooks',
            'ao session ls',
            'ao session get',
            'ao orchestrator ls',
            'ao status',
            'ao project ls',
            'ao project get',
            'ao pty-host'
        )
        OR startsWith(normalized_command_path, 'ao hooks ')
        OR startsWith(normalized_command_path, 'ao session ls ')
        OR startsWith(normalized_command_path, 'ao session get ')
        OR startsWith(normalized_command_path, 'ao orchestrator ls ')
        OR startsWith(normalized_command_path, 'ao status ')
        OR startsWith(normalized_command_path, 'ao project ls ')
        OR startsWith(normalized_command_path, 'ao project get ')
        OR startsWith(normalized_command_path, 'ao pty-host ')
    )
)
GROUP BY day
ORDER BY day
```

For current command adoption, use `ao.cli.invoked` with
`telemetry_schema_version = 3` (and `ao.v2.cli.invoked` for the previous
release), grouped by `command_path` and `actor_type`. Never read
`ao.cli.invoked` without the version filter: the legacy firehose uses the same
name.

For current renderer surface usage, use `ao.routes.viewed` (previously
`ao.v2.renderer.route_viewed`).

For API/UI reliability, union the renderer and daemon reliability events of
both releases:

```sql
SELECT event, count() AS events, uniqExact(properties.install_id) AS installs
FROM events
WHERE timestamp >= now() - INTERVAL 7 DAY
  AND event IN (
    'ao.requests.failed',
    'ao.daemon.failed',
    'ao.sessions.spawn_failed',
    'ao.v2.renderer.api_error',
    'ao.v2.renderer.daemon_failure',
    'ao.session.spawn_failed',
    '$exception',
    'ao.http.5xx',
    'ao.daemon.panic'
  )
GROUP BY event
ORDER BY events DESC
```

## Verification Queries

After enabling ingestion rules, this query should show legacy CLI volume
(no version) falling quickly while version 2 and 3 volume remains:

```sql
SELECT event, properties.telemetry_schema_version AS version,
    properties.actor_type, properties.channel, count() AS events
FROM events
WHERE timestamp >= now() - INTERVAL 24 HOUR
  AND event IN (
    'ao.cli.invoked',
    'ao.app.active',
    'ao.cli.active',
    'ao.v2.cli.invoked',
    'ao.v2.app.active'
  )
GROUP BY event, version, properties.actor_type, properties.channel
ORDER BY events DESC
```

If routine CLI commands still appear in `ao.cli.invoked`, `ao.cli.active`,
CLI-channel `ao.app.active`, `ao.v2.cli.invoked`, or CLI-channel
`ao.v2.app.active` after the rules are enabled, the drop rule is not broad
enough. Check both exact and prefixed shapes
such as `ao hooks`, `AO  HOOKS`, `ao hooks claude-code post-tool-use`, and
`ao session get sess-123`.
