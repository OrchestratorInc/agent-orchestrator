# Accounts Manager Code Atlas Design

## Context

AO is adopting the CLIProxyAPI engine as the foundation of Accounts Manager.
The imported upstream snapshot currently contains 1,390 Go files, including 675
test files, and 37 first-level packages under `internal/`. Important behavior is
distributed across the standalone server, the embeddable SDK, dynamic provider
registrations, authentication selection, protocol translators, executors,
watchers, and storage adapters.

A directory tree or generated call graph alone would not make this codebase easy
to navigate. A full call graph would be noisy around interfaces and dynamic
registration, while a hand-written architecture document would drift as the
upstream snapshot changes. Accounts Manager therefore needs a hybrid code atlas:
deterministic machine-generated inventory plus a small, manually verified map of
the flows and product boundaries that matter to AO.

## Goals

- Give an engineer or coding agent one reliable starting point for navigating
  the imported engine.
- Make the server entrypoints, SDK entrypoints, HTTP routes, package boundaries,
  provider registrations, executors, translators, and authentication components
  searchable without repeatedly rediscovering them.
- Document end-to-end runtime flows that static analysis cannot infer reliably.
- Classify upstream areas as `keep`, `wrap`, `disable`, or `defer` for AO without
  deleting or modifying upstream code.
- Detect atlas drift whenever the upstream engine is updated.
- Keep generated output deterministic, compact, reviewable, and free of secrets.

## Non-goals

- Do not modify or trim `accounts-manager/engine/`.
- Do not integrate Accounts Manager with the AO daemon or frontend yet.
- Do not produce a complete function-level call graph.
- Do not add embeddings, a vector database, an LSP server, or a long-running
  indexing service.
- Do not execute CLIProxyAPI, contact providers, read credentials, or inspect
  runtime account state.
- Do not attempt to replace upstream documentation.

## Layout

The atlas and its tooling live outside the pristine upstream snapshot:

```text
accounts-manager/
├── engine/                         # unmodified CLIProxyAPI snapshot
├── UPSTREAM.md                     # upstream tag and commit
├── atlas/
│   ├── README.md                   # navigation entrypoint
│   ├── COMPONENTS.md               # curated component ownership map
│   ├── FLOWS.md                    # curated end-to-end runtime flows
│   ├── PROVIDERS.md                # curated provider capability matrix
│   ├── AO-SCOPE.md                 # keep/wrap/disable/defer decisions
│   └── generated/
│       ├── SUMMARY.md              # generated high-level inventory
│       ├── PACKAGES.md             # package index and import relationships
│       ├── ROUTES.md               # HTTP route registration call sites
│       ├── REGISTRATIONS.md        # auth/executor/translator registrations
│       ├── packages.json           # structured package/symbol inventory
│       └── imports.mmd             # Mermaid package-group graph
└── tools/
    └── code-atlas/                 # deterministic Go AST generator
```

`atlas/README.md` is the only required starting point. It links to the relevant
generated or curated section for common questions such as “where is account
selection implemented?” and “what receives an OpenAI-compatible streaming
request?”.

## Generator design

The generator is a small standalone Go command under
`accounts-manager/tools/code-atlas/`. It uses the Go standard library parser,
AST, token, and build packages. It does not import the CLIProxyAPI module, load
its dependencies, compile it, or execute initialization code.

The command accepts the engine root and output root explicitly. Its default
paths are repository-relative, but the implementation must not depend on the
current working directory. It scans `.go` files while excluding `.git`, generated
atlas output, vendored dependencies, and binary assets.

For each Go package it records:

- import path and filesystem directory;
- production and test file counts;
- production and test line counts;
- direct internal import edges;
- exported types, interfaces, functions, and methods;
- `main` and `init` declarations;
- source locations for every recorded item.

It also detects a deliberately narrow set of call patterns:

- Gin route and route-group registration calls;
- provider/authenticator registration calls;
- executor registration calls;
- translator registration calls;
- model and plugin registration call sites.

Pattern detection produces source references, not claims about runtime behavior.
If a call cannot be classified confidently, it is listed in an `Unclassified`
section rather than guessed. Dynamic behavior is explained only in curated flow
documents after manual source verification.

Generated Markdown uses repository-relative paths and line numbers. JSON output
uses a versioned schema so future tooling can consume it without scraping
Markdown. All collections are sorted by stable keys before serialization.
Running the generator twice against unchanged source must produce byte-identical
output.

## Package graph

A graph containing every package and edge would be too dense to read. The
generator therefore emits two representations:

1. `packages.json` contains the complete package-level graph.
2. `imports.mmd` groups packages by their first meaningful boundary, such as
   `cmd`, `internal/api`, `internal/auth`, `internal/runtime`,
   `internal/translator`, `internal/store`, `internal/watcher`, `sdk/api`, and
   `sdk/cliproxy`.

The Markdown package index ranks packages by internal fan-in and fan-out. These
metrics are navigation hints only; they are not architectural quality scores.

## Curated maps

### Components

`COMPONENTS.md` explains each major boundary in plain language and identifies:

- what the component owns;
- its public entrypoints;
- its important dependencies;
- the state it reads or mutates;
- the files to inspect first;
- the areas most likely to be affected by changes.

### Runtime flows

`FLOWS.md` documents five initially supported flows:

1. Process startup and service construction.
2. OAuth login and credential persistence.
3. Incoming request, model resolution, account selection, execution, protocol
   translation, and streamed response.
4. Authentication refresh, quota/cooldown handling, retry, and fallback.
5. Configuration or credential file change through watcher-driven reload.

Every step links to a concrete source location. The document clearly separates
verified control flow from architectural interpretation.

### Providers

`PROVIDERS.md` is a provider matrix derived from registrations and then manually
verified. Its rows represent providers and its columns cover:

- login mechanisms;
- credential form and storage adapter;
- executor;
- accepted request protocols;
- upstream protocol;
- streaming and WebSocket support;
- model discovery;
- refresh behavior;
- quota/cooldown participation;
- notable provider-specific behavior.

Unknown or unverified cells remain explicitly `Unverified`; the atlas must not
infer support from filenames alone.

### AO scope overlay

`AO-SCOPE.md` classifies major upstream areas:

- `keep`: core provider, authentication, routing, translation, streaming, model,
  and watcher capabilities required by Accounts Manager;
- `wrap`: behavior AO should control through its own lifecycle, API, storage
  paths, policy, or configuration;
- `disable`: standalone product surfaces that should not be exposed by AO, such
  as public management, TUI, discovery, remote listeners, external control-plane
  integration, and developer diagnostics;
- `defer`: capabilities that remain in the engine snapshot but are not part of
  the first AO integration.

This file records decisions, not deletions. Any later trimming requires its own
reviewed implementation plan.

## Drift and provenance

Generated files include the engine commit from `accounts-manager/UPSTREAM.md`
and a generator schema version, but no wall-clock timestamp. A `-check` mode
generates output in memory and fails if committed output differs.

The generator also emits diagnostics for:

- a changed upstream commit without regenerated output;
- parse failures;
- duplicate package identities;
- registration patterns that were previously classified but are no longer
  found;
- newly found unclassified registration-like calls.

Curated files include a short “verified against” engine commit. Drift checking
warns when that commit differs but does not rewrite curated conclusions.

## Security and privacy

The atlas reads source files only. It does not read `auths/`, configuration
values, environment files, AO state, or user home directories. String literals
are not copied into structured output except for static route paths and known
provider identifiers at recognized registration sites. The generator never
prints token-shaped values or file contents on parse errors.

## Validation

Focused validation consists of:

- unit tests using small Go source fixtures for package, symbol, route, and
  registration extraction;
- determinism test: two generations produce identical bytes;
- malformed source test: failure identifies only the source path and position;
- golden tests for compact Markdown and JSON output;
- `go test` only for the atlas tool module/package;
- one real generation followed by `-check` against the imported engine.

No CLIProxyAPI full test suite, AO full test suite, Docker workflow, provider
login, or network request is required for this navigation-only change.

## Acceptance criteria

- A new contributor can start at `atlas/README.md` and locate the implementation
  of login, request routing, account selection, translation, streaming, token
  refresh, fallback, storage, and watcher reload.
- Generated output is reproducible and `-check` detects source drift.
- All generated facts link back to source locations.
- Curated flow claims are manually verified and do not pretend that AST pattern
  matching establishes runtime behavior.
- `accounts-manager/engine/` remains byte-for-byte identical to the recorded
  upstream snapshot.
- The atlas performs no network or credential access and introduces no runtime
  AO behavior.
