# Cloud GitHub App import from mobile and staging

## Intent and success criteria

Let a signed-in mobile user connect the production-owned AO GitHub App, select an existing authorized repository, and create a project in the Cloud environment currently selected by the app. The same flow must work in the staging desktop development app. Neither staging nor the device receives GitHub App credentials, and a project created in staging must remain in staging. A created project must be able to start a worker and obtain a repository-scoped checkout grant. Keep the current PAT-based manual import available until the App path has been deployed and verified. Do not commit files in `docs/`.

The current default mobile Cloud URL is staging. Staging intentionally has no GitHub App routes; the desktop App picker currently gets a 404 there. Its `POST /orgs/{orgId}/projects` path instead requires a saved PAT. Production owns installation, OAuth, webhook, and repository-grant state. The existing repository-capability broker only prepares *new* scratch repositories, not an already selected repository. Merely reproducing desktop's existing API calls on mobile cannot satisfy the outcome.

## Approaches considered

1. **Recommended: production App plus an existing-repository capability.** Use production for connection and repository selection, then issue a repository- and user-bound capability for a staging project. Staging validates the capability server-to-server and stores it encrypted for worker checkout. This extends the broker already used for scratch projects; it is the only option here that provides both PAT-free import and staging execution.
2. **Mobile UI over staging App endpoints.** Smaller UI change, but staging has no such endpoints by design, so it reproduces the 404 and cannot be tested end to end.
3. **Add a GitHub App to staging.** Would avoid a broker extension, but duplicates credentials, callback state, and webhook ownership, contradicting the existing environment boundary. Rejected.

## Boundaries and flow

The client holds a WorkOS session, not GitHub App secrets. For App setup it constructs a second, fixed-origin Cloud client pointed at `https://api.aoagents.dev` using the same refresh-aware WorkOS token. The staging client continues to use the configured staging URL. Local-password sessions cannot authorize against production and keep the PAT/manual flow. Production and staging organization UUIDs are distinct; the client resolves the first production organization independently, creating a normal user organization there only if one does not yet exist. The production organization is an authorization scope for GitHub grants, not the destination of a staging project.

```text
Mobile or desktop signed in with WorkOS
  -> production: resolve user's org, connect GitHub App, list granted repos
  -> user chooses repo and destination project name
  -> production: issue existing-repo capability for user + staging org + repo + idempotency key
  -> staging: validate capability through the authenticated production broker
  -> staging: atomically create project and encrypt capability in its project row
  -> later worker: staging broker redeems a fresh repo-scoped checkout grant
```

For a production destination, the existing `POST /orgs/{orgId}/github/projects` path remains direct and does not use a cross-environment capability. The existing PAT path remains a separately labeled fallback, not an automatic silent retry after App authorization fails.

## Production control-plane extension

Add an authenticated, idempotent endpoint under the production GitHub routes to prepare an **existing-repository import capability**. Input: GitHub installation ID, GitHub repository ID, target environment (`staging` initially), target organization UUID, and the request idempotency key. The service must confirm current WorkOS principal, production-org administrative membership, GitHub-user connection, live installation access, and an active repository grant for the exact installation and repository. A revoked grant, archived/disabled repository, mismatched owner, or lost user authorization fails closed. It must not create a repository or issue a GitHub installation token to the device.

Reuse the capability reservation/activation and encrypted retry machinery, but bind this import type to the selected repository, target organization, external WorkOS user ID, target environment, and idempotency key. A new migration may add the target-organization/import-kind fields; do not alter merged migrations. Keep scratch capability behavior unchanged. Return the opaque capability and non-secret repository identity under `Cache-Control: no-store`; never log the plaintext capability.

## Destination control-plane extension

Add an authenticated, idempotent project-import endpoint on staging. It accepts the opaque capability, installation/repository IDs, display name, and worker/orchestrator configuration; it does not accept a client-supplied clone URL or branch as authority. The staging principal must be a member of the requested staging org, and its external WorkOS user ID must equal the capability's owner. Staging calls the existing server-to-server broker validator; the validator must return and staging must check target environment, target org, repository/installation IDs, import kind, and idempotency key. Broker failure must not create a project. A rejected/revoked capability must produce a clear authorization error, not a PAT prompt.

In one transaction, store a normal project whose canonical repository URL/default branch come from the validated production repository metadata, and whose remote capability is encrypted using staging's provider key and associated data bound to the project. No worker or orchestrator is started implicitly. Idempotent retries return the same project; a changed payload under the same key fails. Worker checkout uses the existing remote broker and remains scoped to that project's repository. No production database row is used as staging's project record.

## Client and UI

The shared Cloud client gains typed methods for capability preparation and destination import, with generated schema/types kept in sync with the Go API. Mobile's Add Cloud project sheet gains an App connection/repository picker modeled on desktop: connect opens the returned GitHub URL in a browser, the app resumes and refreshes installation state, then loads paginated authorized repositories and permits one selection. The selected repository supplies the default branch; the existing agent selection follows. The screen keeps its native sheet behavior and offers “Set up manually with a GitHub token” as an explicit fallback. Connection cancellation leaves no project behind. A connection completed in an external browser can be detected on foreground/return; polling is bounded and stops on unmount/sign-out.

The desktop development project flow uses a production-origin integration client for GitHub connection/repository listing when its project destination is staging, and uses the same capability/import pair for creation. The Electron main-process bridge continues to own the WorkOS bearer; the renderer never receives it. Production-origin use should be limited to the GitHub integration routes rather than making an arbitrary user-provided origin authoritative. Its current PAT fallback remains available.

## Failure handling and verification

- Expired WorkOS session: reauthenticate, then retry; do not mislabel as a GitHub failure.
- Production integration unavailable or not deployed: show a specific unavailable state and keep manual PAT import available. No silent production project creation.
- GitHub authorization dismissed or repository grant revoked: return to the picker/connection state with a retry; no destination project is written.
- Destination/broker unavailable after capability preparation: preserve the idempotency key for a retry; do not issue a new capability for each tap.
- Account or environment changes during the flow: discard in-memory capability and selection; never send one account's capability under another account's session.

Tests should first fail for endpoint validation, cross-user/cross-org/cross-environment replay, revoked grants, broker downtime, idempotent retries, and canonical repository metadata. Then cover shared-client URLs and errors, mobile browser return and pagination/cancellation, desktop staging routing, and the existing PAT fallback. Verify Go suites, generated API drift, mobile tests/typecheck, frontend tests/typecheck, then manually retest on simulator against a deployed pair of APIs. Local tests can validate the contract before deployment; they cannot prove the current staging deployment works until the new routes are released.

## Non-goals and rollout

Do not configure a second GitHub App in staging, put App secrets or broker tokens in mobile/renderer code, remove PAT support, change worker provider credentials, or start an orchestrator as part of import. This change does not repair existing projects created with a PAT. Deploy production capability issuance and staging capability validation before switching mobile/desktop staging UI to the App path; until then the UI must show the fallback rather than a dead Connect button.
