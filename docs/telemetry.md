# Product telemetry

Orchestrator.inc collects limited product-usage and reliability data to learn which parts of
the app are useful and whether releases are working as expected. Orchestrator.inc does not
attach an email address. Some events include a GitHub project owner or an
authenticated username, so they can identify an account. Other events use a
random installation identifier to group activity. Telemetry is not wholly
anonymous.

Remote telemetry is enabled in production desktop and mobile releases. A
packaged desktop release also enables telemetry for the daemon it starts.
Development builds and daemons started directly do not send remote telemetry by
default.

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
- A random installation identifier and one-way hashes of project or session
  identifiers when an event needs them
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

## Automation usage and creation

When desktop product telemetry is enabled, Orchestrator.inc records visits to the
Automations page with `ao.v2.renderer.route_viewed` and `surface: automations`.
It also records these events when you create an automation:

| Event | When it is sent | Automation-specific properties |
| --- | --- | --- |
| `ao.renderer.automation_create_opened` | You open the create dialog | None |
| `ao.renderer.automation_create_requested` | You submit a form that passes local validation | `project_id_hash`, `schedule_preset`, `prompt_length_bucket` |
| `ao.renderer.automation_create_succeeded` | The create request succeeds | The same properties as the request event |
| `ao.renderer.automation_create_failed` | The create request fails | The same properties as the request event |

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
| `ao.renderer.artifact_create_requested` | You pick the Scratchpad or a project in the Create artifact menu | `scope` |
| `ao.renderer.artifact_create_succeeded` | The session starts | `scope` |
| `ao.renderer.artifact_create_failed` | Starting the session fails | `scope` |

`scope` is `standalone` for the Scratchpad and `project` otherwise. No project
identifier, file name, or artifact content is sent.

The automation events do not include the prompt text, automation name, raw
project identifier, exact schedule, timezone, selected agent, or error message.
Editing an existing automation does not emit these creation events. Closing the
create dialog or failing local validation does not emit a separate event.
Orchestrator.inc uses the creation events to understand completion and abandonment
of the create flow and which schedule presets people choose.

The desktop product-telemetry controls described under "Turn desktop and daemon
telemetry off" also control these events.

## What Orchestrator.inc does not intentionally send

Product telemetry is designed not to include:

- Source code, diffs, commits, or file contents
- Prompts, agent conversations, agent output, or terminal contents
- Shell command arguments, command history, or environment variables
- Repository names, project names, branch names, or plain-text file paths
- API keys, access tokens, passwords, or other credentials
- Names or email addresses

The two explicit account fields are the GitHub owner segment and your
authenticated GitHub username. Both are described under "What Orchestrator.inc sends". The
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
  a personal account. Because Orchestrator.inc uses it to group events, events from that
  installation may be associated with the GitHub handle sent on a session-start
  event.
- Project and session identifiers included in telemetry are one-way hashed.
  Hashing hides the plain text but still allows related events to be grouped.
- Absolute local paths and local application URLs detected in desktop events
  are replaced with redaction markers before the events are sent.
- Daemon events sent to PostHog and mobile events accept a fixed set of
  properties; unexpected fields are discarded.
- Event rates are limited to reduce repeated background activity and error
  loops.
- Session recording is disabled in the desktop and mobile apps. Orchestrator.inc does not
  automatically record screens, clicks, or touches.
- Person profiles are off for every event except the session-start event that
  carries your GitHub handle. That one event sets a person property so activity
  can be grouped by GitHub username, and clears build details (version,
  platform, surface, build mode) that older Orchestrator.inc versions stored on the profile;
  other events do not set a person profile. Project-add events still carry the
  GitHub owner segment described above, so this is not an anonymity guarantee.

Separately from remote telemetry, the daemon can keep a local copy of
operational events in Orchestrator.inc's SQLite database. While local telemetry is active, Orchestrator.inc
periodically prunes records older than 30 days. This data stays under `~/.ao` on
your machine.

## Sharing your GitHub handle

Orchestrator.inc resolves the GitHub account signed in to its GitHub integration and includes
that username on session-start events. It is sent both as the event property
`github_actor` and as a PostHog person property on Orchestrator.inc's shared installation
person, which lets us group product activity by GitHub username and reach out to
active users for feedback. Other events from the same installation can be
associated with that person in PostHog.

Orchestrator.inc only sends the handle when the signed-in account is a personal (human)
account; it never sends an organization or a bot token, and if no GitHub token is
available it sends nothing. The handle is part of product telemetry and has no
separate switch: turning telemetry off (see below) stops it, because the
session-start event that carries it is then never sent. Anything already stored
in PostHog from earlier events is not deleted retroactively.

## Turn desktop and daemon telemetry off

The desktop General Settings page includes an **Event reporting** control for
desktop and daemon product telemetry. The environment-variable controls below
continue to govern those telemetry paths.
Set all three variables in the environment used to launch Orchestrator.inc:

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

These environment variables do not control the mobile app. The current
production mobile app does not provide an in-app telemetry opt-out. Turning
desktop or daemon product telemetry off stops new collection there; it does not
delete events already sent to PostHog, remove the local installation identifier,
or delete existing local product-telemetry records. Automatic deletion of those
local records older than 30 days resumes if daemon event capture is enabled
again.

## Questions or corrections

For the broader data policy, retention information, and contact options, see
the [Orchestrator.inc privacy policy](https://orchestrator.inc/privacy). You can report a problem
with this documentation in the
[GitHub repository](https://github.com/OrchestratorInc/agent-orchestrator).
