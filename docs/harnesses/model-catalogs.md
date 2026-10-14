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
