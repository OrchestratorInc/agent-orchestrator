# Provider browsing in Harness settings

Settings → Harness → Models retains all reported providers, including providers
with no models. Selecting a provider shows its models. Multi-provider harnesses
can be browsed before login. A **Configured** marker means AO observed credential
configuration; it does not mean the provider accepted the credential. Missing
markers mean unknown configuration, and never hide providers.

The daemon combines native model output with these local sources:

| Harness | Additional catalog source | Configured evidence |
| --- | --- | --- |
| OpenCode | Its XDG `opencode/models.json` cache | Native auth JSON, known environment keys and provider-declared environment keys |
| Kilo Code | Its XDG `kilo/models.json` cache | Native auth JSON and provider environment keys |
| MiMo Code | Its XDG `mimocode/models.json` cache | Native auth JSON and provider environment keys |
| Pi | The installed Pi package's generated model catalog, `models-store.json`, and user `models.json` | Native auth JSON and known environment keys |
| Kimchi | Its embedded Pi runtime's `models-store.json` and user `models.json` | Unknown unless a reliable source is added |
| OMP | Read-only `models.db` model cache | Enabled credentials in the native auth database; legacy JSON only when the database is absent |
| OpenCode 2 | Native command output | Known environment keys; legacy OpenCode auth JSON is not reused |
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
