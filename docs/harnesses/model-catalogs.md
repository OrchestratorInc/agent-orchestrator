# Shared harness model catalogs

Settings → Harness → Models, New Task, project role settings and reviewer
pickers use one persisted catalog for each harness and scope. The daemon's
`loadModels` path discovers, refreshes and saves it; `Discoverer.Discover`
combines the harness-specific sources. There is no separate Settings catalog
or `BrowseAll` switch.

The shared catalog includes native command/configuration/ACP/app-server output
and the broader provider sources already supported by each harness:

- OpenCode, Kilo Code and MiMo Code: their own native model caches.
- Pi, Kimchi and Prime Agent: their supported local model stores, custom
  configuration or installed package catalogs.
- OMP: its read-only native model database.
- FX: Gateway, Codex, Grok and declared custom connection models.

Claude models come from the configured provider. Failed discovery retains the
last cached catalog for matching discovery inputs; no hardcoded Claude list is
substituted. Without a matching cache, manual model entry remains available.
A successfully discovered catalog also preserves an explicitly configured model.

Native model caches may include providers without configured credentials.
A **Configured** marker means AO observed credential configuration; it does
not prove that the provider accepts it. Providers with no models are omitted
from Settings. Settings shows the full catalog, including disabled models;
its enable/disable choices feed the shared `useOfferedModels` filter used by
pickers. Already selected models remain visible. Live chat controls retain
the session protocol's supported choices and apply the same filter.

FX selections carry a connection-qualified AO ID, such as
`fx:codex:openai/shared`, because two connections may expose the same native
model ID. Launch and restore decode it into process-local `FX_PROVIDER` and
`FX_MODEL` overrides. Existing raw model overrides keep their previous
behavior. Discovery never changes FX's saved provider selection. A partial
refresh updates successful connections and retains previously cached models
only for connections that failed, with a warning.

Cached reads return immediately; initial discovery is needed only when that
scope has no saved catalog. Background refreshes and explicit refreshes use
the same pipeline. Database change events notify Settings and all project
pickers that may be reading the global catalog. Older `@browse:all` rows are
ignored during warmup and invalidation.

Project and cloud credential scopes retain their boundaries. Credential-scoped
discovery does not add device-local provider catalogs. Settings omits the
session-history recency lookup that task pickers use, so frontend query keys
can differ while the underlying catalog stays shared. Existing picker display
ordering is preserved.

## Cloud tasks

New Task in a Cloud project lists the control plane's catalog for the agent
(`GET /api/cloud/v1/orgs/{orgId}/providers/{agent}/models`), never this
computer's, so a missing or expired local login does not empty the picker. The
list is what the next task's worker runs: either a Cloud worker on the same
CLI version and the user's current credential reported it (`source: worker`,
"Verified on your Cloud workers"), or it is the list the control plane's pinned
CLI version supports (`source: release`, "Models for Claude Code 2.1.287"). It
needs no live worker.

The picker opens on the project's or the agent's default, which sends no
model; no listed model is picked for the user. A configured, remembered or
typed model stays selectable even when the catalog does not list it, with a
warning that it may not run in Cloud. Any model ID can be entered.

A listed model takes only the effort levels the catalog reports for it, so a
model that reports none (Claude's Haiku) starts with no effort; Claude refuses
a turn whose effort its model does not offer. A model the catalog does not
list keeps the chosen effort.

When the control plane has no catalog:

- 409 `MODEL_CATALOG_UNAVAILABLE` (OpenCode today): OpenCode keeps its
  discovery scoped to the connected provider's credential type; other agents
  offer the default and manual entry.
- 404 or 501 without an error code (a control plane that predates the route):
  this computer's models appear as unverified suggestions, never selected
  automatically.
- Any other failure: the default and manual entry, with choices already made
  kept.
