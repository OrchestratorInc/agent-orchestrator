# 7. Computer use on another of the user's computers

Date: 2026-11-21
Status: Proposed

## Context

Computer use runs Cua Driver on the machine where the agent runs. Users want
to keep an agent on one computer (a Linux box, a VM, the machine with the
checkout) and have it operate the desktop of another computer they own (the
Mac with the signed-in apps).

The user's computers can already reach each other: a machine shared with
"Allow access from your devices" or `ao remote-host enable` runs the
authenticated remote-host listener, over the LAN or a Cloudflare tunnel, and
the desktop app keeps a credential for every saved host.

`/api/v1/computer-use` and `/api/v1/settings/computer-use` are on that
listener's blocked list (`lanControlBlockedPrefixes`): a paired phone must not
drive the desktop or turn on agent computer use. AGENTS.md says the listener
never serves loopback-gated control routes.

## Decision

Add one new route prefix, **`/api/v1/remote-computer-use`**, that the remote-host
listener serves behind `authMiddleware` like any app route, and keep everything
else about computer use loopback-only.

On the computer that is used:

- A new setting, "Let agents on your other computers use this computer"
  (`PUT /api/v1/computer-use/remote/allow`, loopback-only), is off by default
  and needs Cua Driver installed to turn on.
- `GET /api/v1/remote-computer-use/status` reports whether it is offered now.
- `GET …/agent-tools` and `POST …/call` refuse with `COMPUTER_USE_REMOTE_OFF`
  until the setting is on, and calls also need "Let agents use this computer"
  on. They run through the same `Call` path as local calls: the same tool
  allowlist, argument checks, foreground refusal and screenshot folder rules.
- Screenshots come back base64-encoded in the response, since the caller cannot
  read this computer's files. Each is capped at 10 MB.
- Calls from other computers keep their own screenshot folder and Cua Driver
  session (`remote-<caller>-<session>`), so they never mix with local sessions.

On the computer the agent runs on:

- A new setting, "Run computer use on", names one saved remote host
  (`PUT`/`DELETE /api/v1/computer-use/remote/target`, loopback-only).
- The desktop app's main process sends that host's address and the credential
  it already holds for it straight to the local daemon after verifying the
  host's identity. The renderer only names the host and never sees the
  credential, as with every other remote-host credential.
- The daemon stores the target in `<dataDir>/computer-use-remote.json`
  (mode 0600). No API response returns the credential.
- `ao mcp`'s calls to `/api/v1/computer-use/call` and `…/agent-tools` are
  forwarded to the target with `Authorization` and `X-AO-Expected-Host-ID`.
  Redirects are not followed. Screenshots are saved under the local session's
  folder and the result's paths rewritten to them, so the agent's tools work
  unchanged.
- New chat sessions get the computer-use tools while a target is set, even with
  "Let agents use this computer" off or no Cua Driver here, and their standing
  prompt names the other computer.

## Consequences

Any device that can authenticate to a machine as a remote host can drive that
machine's desktop once the user turns the new setting on. That includes
devices using the pairing password, per-device tokens, and the account token
shared by devices signed in to the same Orchestrator account. The setting's
description says so. `ao remote-host require-device-credentials on` and
`ao remote-host devices revoke` limit and revoke that set as before.

Tunnel traffic, screenshots included, is readable by Cloudflare, as all tunnel
traffic already is.

The settings that decide whether a machine can be used, and where its own
computer use runs, stay loopback-only and renderer-origin-only. A paired device
cannot turn either on. Browser origins other than the renderer cannot call the
new prefix (`rendererOnlyArea`).

The daemon now holds a copy of one remote-host credential. If the host rotates
it, calls fail with `COMPUTER_USE_REMOTE_UNAUTHORIZED`, which tells the user
to choose the computer again.

AGENTS.md's LAN-listener rule is unchanged in spirit: the new prefix is an
authenticated app route that refuses all work by default, not a control route.
