# Provider browsing in Harness settings

Settings → Harness → Models retains all reported providers, including providers
with no models. Selecting a provider shows its models. Multi-provider harnesses
can be browsed before login. A **Configured** marker means AO observed credential
configuration; it does not mean the provider accepted the credential. Missing
markers mean unknown configuration, and never hide providers.

For Settings browsing, the daemon combines native model output with these
local sources. New Task, role settings and other model selectors use a separate
catalog with native output only, so they offer just the providers the harness
can run now:

| Harness | Additional catalog source | Configured evidence |
| --- | --- | --- |
| OpenCode | Its XDG `opencode/models.json` cache | Native auth JSON, known environment keys and provider-declared environment keys |
| Kilo Code | Its XDG `kilo/models.json` cache | Native auth JSON and provider environment keys |
| MiMo Code | Its XDG `mimocode/models.json` cache | Native auth JSON and provider environment keys |
| Pi | The installed Pi package's generated model catalog, `models-store.json`, and user `models.json` | Native auth JSON and known environment keys |
| Kimchi | Its embedded Pi runtime's `models-store.json` and user `models.json` | Unknown unless a reliable source is added |
| OMP | Read-only `models.db` model cache | Enabled credentials in the native auth database; legacy JSON only when the database is absent |
| OpenCode 2 | Native command output | Known environment keys; legacy OpenCode auth JSON is not reused |
| FX (Harness Settings only) | Native `fx models --json` for Gateway, Codex and Grok using process-local `FX_PROVIDER`; custom `model_metadata` from `~/.fx/settings.json` | `connected_providers` from native `fx status --json` |
| Other multi-provider harnesses | Native command, config or ACP output already used by their adapter | Unknown unless the adapter reports reliable evidence |

Catalogs remain owned by the installed harness. If a cache is absent, unreadable
or from an unsupported version, AO keeps the native results. Standalone Pi
binaries without an importable JS package use their native command and local
JSON catalogs. These fallbacks may report fewer providers than a full installed
registry; AO does not substitute another harness's catalog or invent models.
Model IDs retain their native provider and namespace syntax.

Credential-scoped cloud discovery keeps its existing scope and does not merge
local catalogs. Catalog-only credential placeholders never produce a Configured
marker. Local cache changes invalidate previously cached AO catalog results.

FX browsing uses a separate persisted catalog and frontend query key. New Task,
role settings and other model selectors continue using FX's active provider.
Browsing never runs `fx provider` or changes the saved provider/model selection.
The built-in providers remain visible while disconnected; private subscription
catalogs still require provider access. On a partial refresh, successful providers
update and failed providers retain their previous cached models with a warning.
The connection name is explicit metadata: native model IDs are not rewritten or
split into providers. Custom connection models come from the profile's declared
metadata, including context windows; AO does not send requests to custom URLs.

References: [FX models](https://fx.sh/docs/configure-fx/models),
[authentication](https://fx.sh/docs/getting-started/authentication), and
[custom connections](https://fx.sh/docs/configure-fx/custom-model-connections).
