# Mobile cloud environments — design

Branch: `mobile/work-ahead` (from `untrivial/main` `e9080b965`).
Status: design approved in session 2026-09-19. Not yet implemented.

## 0. What this builds

Mobile gains a second environment. Today `packages/mobile` is a remote control
for one local daemon: it pairs with a Mac, races LAN/Tailscale/tunnel endpoints,
and dies when that Mac sleeps. After this work the user chooses an environment —
**Local** (today's paired daemon) or **Cloud** (the hosted control plane in
`cloud/`) — and sees that environment's projects and sessions.

Pass 1 scope: the environment switcher, cloud sign-in, cloud projects and
sessions on the board, spawning a cloud session, chatting with it, and the
pause/resume lifecycle. Explicitly deferred in §9.

## 1. Ground truth (verified 2026-09-19)

Several repo documents are stale. What follows was read from the code.

- **The cloud control plane is in this repository**, at `cloud/` — Go, ~24
  handler files under `cloud/internal/httpapi/`. `private/ao-cloud` is an empty
  directory with `update = none` in `.gitmodules`; it is vestigial.
  `docs/cloud-development.md` and `docs/cloud-refactor.md` still describe the
  private-submodule era and should not be trusted on this point.
- **Hosted execution works.** `cloud/docs/execution-readiness.md` has every
  provisioning, worker, and orchestration gate checked. Sandbox providers are
  `docker`, `coder`, and `createos` (`cloud/internal/sandbox/`), with NodeOps
  harness images for Claude Code, Codex, and Cursor (`cloud/nodeops/harness/`).
  The claim in `docs/cloud-development.md` that hosted execution is unbuilt is
  wrong.
- **The server is ahead of the public contract.** `contracts/cloud/openapi.yaml`
  does not describe routes that exist in `cloud/internal/httpapi/server.go`:
  `sessions/wake`, `sessions/{id}/resume`, `sessions/{id}/restore`, share links
  and grants, org members and invitations,
  `provider-connections/agents/{agent}/promote`, the browser proxy,
  `worker/reconnect`, `worker/work/wait`. Because `packages/cloud-client` is
  generated from that contract, **the client is incomplete by construction.**
- **There is no device or machine concept in `cloud/`.** Grepped both the
  contract and the implementation. Account-based daemon discovery (no QR scan)
  is new surface — deferred here, see §9.
- **Cloud sessions pause themselves.** `cloud/internal/idlepause` stops a
  sandbox after a quiet period with no user message and no turn in flight.
- **`packages/cloud-client` is React Native safe** except for `streamEvents`,
  which uses `response.body.getReader()` and `TextDecoder`
  (`packages/cloud-client/src/client.ts:460`). That is the one method this
  design does not use.

### How desktop implements cloud

Mobile mirrors the logic, not the mechanism. Desktop's layers:

| Layer | Desktop | Mobile |
| --- | --- | --- |
| Gate | daemon-resolved; `settings/service.go:66` `cloudEnabled = (AO_CLOUD_OFFERING \|\| toggle) && controlPlaneURL != ""` | mobile owns its own gate — a phone with no paired Mac has nothing to ask |
| Auth | `frontend/src/main/cloud-auth.ts`: WorkOS PKCE, `safeStorage` → `cloud-auth.bin`, single-flight refresh | `expo-auth-session` + `expo-secure-store`, same state machine |
| Transport | `frontend/src/main/cloud-cp-proxy.ts` — IPC proxy, because the CP has no CORS and the token must stay out of the renderer | **deleted.** RN has no CORS and no renderer/main split |
| Client | `frontend/src/renderer/lib/cloud-cp/client.ts` with a `"delegated-to-main-process"` placeholder token | `@aoagents/cloud-client` with a real token |
| Org | `useCloudOrg`: first org from `/me`, else create. No switcher | identical |
| Fusion | `useWorkspaceQuery.ts:388` merges cloud into the local board, `kind: "cloud"` | **switcher instead of merge** — see §6 |
| Lifecycle | `renderer/lib/cloud-lifecycle.ts`, pure | ported verbatim, given real UI |
| Terminal | `renderer/lib/cloud-terminal-mux.ts`, ticketed `wss` | deferred to pass 2 |
| Onboarding | `CloudOnboardingGate`, `CloudCredentialDialog` | ported, see §7 |

## 2. Architecture — the environment port

Mobile threads `cfg: ServerConfig` through every data call; `lib/chat/api.ts`
alone has ~40 such functions. Threading a second config type through all of them
would be a large, risky refactor of recently shipped code.

Instead the UI depends on a port. This follows the rule desktop already states
in `docs/cloud-refactor.md`: *"Shared views receive data and actions from their
host. They do not select a transport internally."*

```
packages/mobile/lib/environment/
  types.ts     SessionSource — the interface both environments implement
  local.ts     adapter over today's ServerConfig functions; no behavior change
  cloud.ts     the same interface over @aoagents/cloud-client
  store.tsx    which environment is active; persisted in AsyncStorage
```

`SessionSource` covers exactly pass 1's needs and no more:

```ts
interface SessionSource {
  listProjects(): Promise<ProjectInfo[]>;
  listSessions(): Promise<DashboardSession[]>;
  createSession(input: SpawnOptions): Promise<{ id: string }>;
  deleteSession(id: string): Promise<void>;
  getConversationPage(id: string, opts): Promise<ConversationPage>;
  sendMessage(id: string, input: SendMessageInput): Promise<SendMessageResult>;
  cancelTurn(id: string, turnId: string): Promise<void>;
  subscribeEvents(id: string, listener): () => void;
}
```

`local.ts` is a pure adapter: it forwards to the existing functions with the
active `ServerConfig`. The local path keeps its current code and its current
tests. **This lands first, as a no-op, with the suite green, before any cloud
code exists** (§8).

## 3. Authentication

`lib/cloud/auth.ts` ports the logic of `frontend/src/main/cloud-auth.ts`:

- WorkOS AuthKit with PKCE via `expo-auth-session`. The AuthKit client id is
  public configuration (it appears in every sign-in URL), so it is baked with an
  env override, exactly as desktop does at `cloud-auth.ts:23`.
- Redirect `aomobile://callback`. The scheme is already registered for deep
  links; note `com.prasadware.ao.mobile.dev` can intercept these on a simulator
  with a dev build installed.
- Tokens in `expo-secure-store` (iOS Keychain / Android Keystore), never in
  AsyncStorage — the same rule `lib/config.ts` already applies to the daemon
  connection password.
- Single-flight refresh plus the generation counter that invalidates in-flight
  operations on sign-out, ported from `cloud-auth.ts`.
- The UI receives the token-free `CloudAccount` projection from
  `frontend/src/shared/cloud-account.ts`.

Local email/password (`POST /auth/local/register`, `/auth/local/login`) is
supported by the same code path: a bearer token from a different endpoint. It is
how this work is developed against `npm run cloud:local` without WorkOS, and it
mirrors desktop's `cloud-auth-local.ts` / `CloudLocalSignInDialog.tsx`. Like
desktop, it is restricted to loopback/dev control planes.

Org resolution copies `useCloudOrg` exactly: first org from `GET /me`, else
`POST /orgs` named from the account. No org switcher (§9).

## 4. Client and contract

Mobile consumes `@aoagents/cloud-client` directly. It does **not** port
`frontend/src/renderer/lib/cloud-cp/client.ts` — that client exists to cope with
the IPC proxy — and it does not add a third client.

Because the contract lags the server (§1), this pass regenerates
`contracts/cloud/openapi.yaml` to cover at minimum the routes pass 1 depends on:
`POST /orgs/{orgId}/sessions/wake`, `POST /orgs/{orgId}/sessions/{id}/resume`,
`POST /orgs/{orgId}/sessions/{id}/restore`. Follow the API-change loop in
`AGENTS.md`; commit the spec and the regenerated `schema.ts` together. Desktop
and the web UI benefit from the same fix.

Expo consumes the package by source path through Metro, matching how the repo
already links `packages/*`. Verified on device early (§8), because Metro
resolution of workspace packages has broken this app before.

## 5. Data flow and liveness

Cloud liveness is **cursor polling**, not SSE. RN has no `EventSource`, and
`expo/fetch` streaming hits a JNI global-reference ceiling that aborts
long-lived streams. This is not a workaround invented here: mobile already polls
rather than streams on the Cloudflare tunnel path (ADR 0004), and
`lib/chat/eventCursor.ts`, `lib/chat/conversationPoll.ts` and
`lib/pollInterval.ts` already implement cursor persistence, head-start
semantics, and adaptive intervals.

`GET /orgs/{orgId}/sessions/{id}/chat-events?after=` is the same cursor shape as
the daemon's stream. `cloud.ts` reuses the existing machinery with the cursor
keyed on `orgId:sessionId` rather than `machineIdentity`.

`lib/cloud/mapping.ts` maps cloud DTOs onto mobile's existing `DashboardSession`
and `ProjectInfo` types, so the board, cards, status colors, and chat timeline
render unchanged. Fields with no cloud analogue take safe defaults, the way
`toCloudWorkspace` does on desktop.

## 6. Lifecycle — pause and resume

`cloud/internal/idlepause` pauses a sandbox after quiet time. On a phone this is
the most visible cloud behavior: the user opens the app hours later and the
session is cold. Desktop under-serves this; mobile cannot.

Port `frontend/src/renderer/lib/cloud-lifecycle.ts` verbatim — it is pure, and
maps `desiredState`/`observedState` to `paused_by_coder`, `resuming_workspace`,
`waiting_for_coder_agent`, `starting_ao_worker`, `restoring_agent`, `connected`.

Give those stages real UI: a paused session reads "Paused — tap to resume";
tapping calls `/resume`; the chat shows the intermediate stages and keeps the
composer locked until `connected`. Without this the app looks broken.

## 7. Onboarding, the switcher, and errors

First run asks **which environment** rather than going straight to pairing:

- **Cloud** → sign in → org resolves → board. No Mac, no scan, no QR.
- **Local** → today's pairing flow, untouched.

Both may be configured; the switcher lives in the drawer. Each environment
renders its own honest empty state — Local says the Mac is unreachable rather
than showing an empty board. `lib/onboarding.ts`'s `shouldOnboard` gains an
environment dimension while keeping its "unknown means do nothing yet" rule.

Error handling:

- **401** — clear the token, drop to signed-out, never loop. Same terminal state
  desktop treats it as.
- **Environment unreachable** — an environment-scoped banner reusing
  `lib/connectionError.ts` and `StaleBanner`. Must never be confused with
  "daemon unreachable"; the two environments report independently.
- **No agent credential** — cloud `createSession` returns 422 when the org has
  no valid provider connection for the chosen harness. Port
  `CloudOnboardingGate`'s once-per-sign-in prompt and block spawn behind a
  "connect a coding agent" CTA rather than eating the 422. Note the gate/server
  mismatch recorded in `docs/superpowers/specs/2026-09-11-cloud-onboarding-design.md`
  §2.4: check the connection for *that specific harness*, not "any valid key".
- **Cursor divergence** — reuse the reset path the daemon stream already has.

## 8. Testing and sequencing

Mobile's suite is `vitest` over pure modules, which fits this design:
`mapping.ts`, the cursor logic, `cloud-lifecycle.ts`, and the auth state machine
are all pure. `SessionSource` gets a fake implementation so screen-level tests
run against both environments.

Order of work, each step independently green:

1. `SessionSource` + `local.ts` as a pure no-op adapter. Existing tests pass
   unchanged. Nothing cloud exists yet.
2. `cloud-client` consumption verified on a real device — the Metro risk, proven
   before anything is built on it.
3. Contract regeneration for wake/resume/restore (`AGENTS.md` API loop).
4. `lib/cloud/auth.ts` against `npm run cloud:local` with local email/password.
5. `mapping.ts` + `cloud.ts` read paths; cloud board renders.
6. Spawn, chat, cancel.
7. Pause/resume lifecycle UI.
8. Environment onboarding and switcher.

Gates: `npm run typecheck` and `npm test` in `packages/mobile`; `go test ./...`
in `cloud/` if any control-plane code is touched; device verification against
`npm run cloud:local` for each of steps 4–8.

## 9. Out of scope for this pass

- Terminal for cloud sessions (ticketed `wss` to `/api/cloud/v1/terminal`,
  protocol 2). `cloud-terminal-mux.ts` is the reference; mobile's
  `lib/mux.ts` + xterm webview is the target.
- Workspace files and diffs; PRs and reviews for cloud sessions.
- Push notifications originating from cloud. Today's push is daemon-registered
  (`backend/internal/httpd/controllers/push.go`); cloud has no path to the phone.
- **Account-based access to the local daemon** (no QR scan). The intended design
  is a machine directory in `cloud/`: the daemon self-registers
  `{hostId, name, advertised endpoints, per-device credential}` under the
  account using the WorkOS token Electron main already holds, and the phone
  lists the user's machines and connects **directly** over the existing endpoint
  race — cloud as a phonebook, not a relay, so ADR 0004's privacy tradeoff does
  not get worse. Constraint recorded for whoever picks this up: the phone must
  **not** receive the shared connection password. That password authorizes
  terminal input (`backend/internal/httpd/auth.go`), so account-scoped access
  must mint revocable per-device credentials with a device list in desktop
  settings; otherwise one leaked account session is full machine compromise.
- Org switching, sharing, and invitations, all of which the control plane
  already implements.
