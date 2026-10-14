# Pool ACP qualification hold

Pool is **unregistered and unavailable in AO**. This draft contains guarded,
source-only metadata and ACP preparation. Terminal launch/restore and all ACP
launches hard-error. The five nativeacp default capabilities are explicitly
false. No registry, API enum, database migration, picker, reviewer or interface
handoff is added. The proprietary executable is not redistributed.

## Released native evidence

On Ubuntu Linux amd64, official [Pool v1.0.16](https://github.com/poolsideai/pool/releases/tag/v1.0.16)
was the latest published release when checked on 2026-10-11.

- Archive SHA-256: `e86aa8c9feef003540673ab494e91bfadc273218d531c43d662cafb69e464146`.
- Executable SHA-256: `db70340b41c8bd76dbe2b5926a6e46cda804789b9774b3d4cfba3591f0de02f2`.
- Native `--version` returned `1.0.16`; initialize identified `pool-acp` version
  `1.0.16`, protocol 1, loadSession true and no authMethods in standalone mode.
- `session/new` returned a provider-issued native ID and default mode plus
  accept-edits/always-allow configuration choices. These declarations do not
  establish authorization, permissions or lifecycle conformance.
- An unknown UUID passed to `session/load` returned JSON-RPC -32603 with
  session-not-found instead of creating new history. Actual replay remains unproved.

## External prerequisite

The existing authorized VPS Z.ai coding credential was supplied only through
`POOLSIDE_API_KEY`, with `POOLSIDE_STANDALONE_MODEL=glm-4.7`. A native ACP prompt
request with no tool usage failed with JSON-RPC -32603 and HTTP 404. Native logs
showed Pool requesting `/v4/v1/models` and `/v4/v1/chat/completions` when configured
with `https://api.z.ai/api/coding/paas/v4`. A separate native attempt using the
explicit `/v4/v1` base also failed with 404; it did not repair the routing.

A direct authorized provider routing control at
`https://api.z.ai/api/coding/paas/v4/chat/completions` returned HTTP 200, one choice,
and model `glm-5.3-flash` at 2026-10-11T16:21:17Z. This proves that the available
credential and supported provider route worked then; it is **not Pool or AO
conformance**. No credential value or response transcript is published.
The audit account has no configured DeepSeek environment key or saved Pool
login in the isolated profile. Existing Codex login has no established Pool
import path. No account signup, paid upgrade, gateway, native patch or security
bypass was used to manufacture success.

[Pool login docs](https://docs.poolside.ai/get-started/log-in) support compatible
providers; [installation docs](https://docs.poolside.ai/cli/install) describe
arbitrary required base paths. The pinned native observations above take
precedence over assuming that this documented route works with Z.ai. The next
qualification needs a released native fix for this provider path or an already
authorized endpoint/credential that works with Pool's emitted requests.

## Guarded preparation

The unregistered metadata adapter discovers a user-installed binary and reports
key presence as configured, never authorized. Missing credentials remain unknown.
Explicit empty session environment overrides do not inherit a daemon key.
Terminal restore hard-errors rather than falling through to fresh history.

The pure POSIX helper/settings builder prepares an unnamed additive SessionStart
hook using `additional_context`. It preserves user/default prompts and hooks;
`prompts.system_message` is never set. It rejects invalid UTF-8, NUL, nonabsolute
paths and instruction/serialized-output bounds, including JSON escaping growth.
This is source preparation only: no helper/config artifacts are installed or
executed by the driver. [Native hooks](https://docs.poolside.ai/hooks) fail open
and cap displayed context at 32 KiB. Actual hook invocation, consumption on
begin/load, failure detection, privacy and artifact cleanup still need proof
before any launch is enabled. The conservative serialized bound does not prove
native marker/wrapper accounting or instruction delivery.

## Verification and ownership

Focused test source covers auth classification, hard TUI/restore refusal,
launch rejection, disabled capabilities, additive settings, payload round-trip
and size/escaping failures. Routine tests, lint, typecheck, vet and workflow
suites were **not run locally**; validation belongs exclusively to PR GitHub CI.
Formatting was applied without running those suites.

The three owned native ACP PIDs exited after bounded SIGTERM/SIGKILL cleanup and
were confirmed absent. These idle/failed-turn shutdown observations do not
prove executing-shell cancellation. Native profiles/history, preparation
checkout and earlier evidence were preserved; production AO was untouched.
All native audit XDG data lived under an explicit isolated `~/.ao` path.

Native tool turns, permissions, private/project instruction consumption,
active-to-settled behavior, exact history replay, shell cancellation, and all AO
nativeacp/API/Chat/Kill/reconnect gates are **NOT_RUN**. No AO session was launched
and no actual AO Electron screenshot exists. Screenshot status is unavailable
because native provider qualification failed before safe production admission;
there is no substitute PNG and no integration claim.

## Published scope

Guarded source draft: [PR #6551](https://github.com/OrchestratorInc/agent-orchestrator/pull/6551).
[Curated native qualification record](assets/pool-20261011/qualification.json) contains
artifact hashes, redacted native identity digests, native responses and exact owned
cleanup state. Original bounded observations remain on the audit VPS; credentials,
profiles, databases, complete raw logs and native executable are excluded.

This evidence append does not change the audit runner; its SHA-256 remains
`8887aa2e7101b9e6d3950bfca08ffddf2e1e6c1c3f1d030c8dec2676443aecea`.
Earlier fixture CI is historical until CI validates this evidence head.
