# ZCode adapter: public integration deferred

ZCode is **not registered, selectable, installable or offered for authentication
through AO**. The retained adapter, hardened auth probe and configuration parser
are staged development work, not a qualified worker/orchestrator integration.
Historical rows retain their harness value, logo and install-job wire identity.

## Why registration is deferred

Every normal AO worker and orchestrator receives standing instructions. The
pinned ZCode interface has no qualified private delivery channel that preserves
the native user environment. The adapter rejects nonempty inline/file
instructions, including restore fallback, rather than silently dropping them.
Unreadable instruction files return an actionable error.

The attempted session-scoped HOME overlay was removed. At upstream revision
`3c5b227649371507ddc4eb46ac5133afe6db1cab`,
[file-config.adapter.ts](https://github.com/zai-org/ZCode/blob/3c5b227649371507ddc4eb46ac5133afe6db1cab/apps/zcode-cli/packages/adapters/src/config/file-config.adapter.ts)
resolves `~/.zcode/cli/config.json` through `node:os.homedir()`. Pinning
`ZCODE_STORAGE_DIR` does not preserve that independent lookup. Redirecting HOME
also hides native `~/.gitconfig`, changes child-tool home resolution, and hides
the user's existing `~/.zcode/AGENTS.md`. The adapter no longer changes HOME,
native instructions, provider configuration or global Git configuration.

## Retained behavior and compatibility

- The unregistered adapter can construct an interactive command when no AO
  instructions are supplied. Permission modes retain ZCode's documented ladder.
  This private command-construction behavior does not enable a normal AO role.
- Explicit AO model overrides remain rejected. Production/base/fallback discovery
  publishes no selectable or custom model IDs, and task/settings reads suppress
  stale cached choices. The author's provider-runtime/personal-model parser is
  retained and independently tested.
- The staged auth probe requires a successful provider turn and matching final
  result; session metadata and prompt echoes do not prove authentication. Probe
  workspaces remain beneath AO_DATA_DIR and are cleaned up. This code is not
  offered as a public setup/login flow while registration is deferred.
- Persisted harness data, migration history and display identity remain readable.
- Hooks, native-conversation restore, Chat and reviewer integration are not
  implemented. No HOME/config overlay or visible first-message instruction
  fallback is retained.

## Qualification required before enabling

A future integration must prove a supported private instruction channel that
preserves native user configuration, native user instructions and child-tool HOME
behavior. Manager-level worker and orchestrator tests must show instructions
reach the real harness, followed by live setup/authentication, first/follow-up
prompts, role compliance, activity, usage and kill/relaunch/history qualification.
A valid session-scoped model selection channel is separately required before AO
can advertise model IDs.

Hosted regression coverage for this deferral checks manager rejection before
session/workspace/runtime state, inline/file/restore instruction guards, and a
child-process launch boundary with temporary native-home config, Git identity
and credential-helper settings, and existing user AGENTS.md. That fixture does
not execute ZCode or constitute live integration qualification. The earlier real
audit/screenshots remain historical failure evidence; they do not qualify this
change.
