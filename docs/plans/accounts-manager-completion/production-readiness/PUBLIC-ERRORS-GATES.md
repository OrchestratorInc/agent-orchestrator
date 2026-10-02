# Gates: durable public failure codes

OWNS: backend/internal/httpd/controllers/accounts_manager_controls*, backend/internal/httpd/apispec/openapi.yaml, backend/internal/cli/accounts_manager*, backend/internal/cli/session_account*, frontend/src/api/schema.ts, frontend/src/renderer/lib/accounts-manager-controls*, frontend/src/renderer/components/SessionAccountControl*, frontend/src/renderer/components/settings/AccountRemovalControl*, frontend/src/renderer/i18n/*.json, docs/plans/accounts-manager-completion/production-readiness/PUBLIC-ERRORS-*.md

Scope: safe durable diagnostics across public clients without changing execution or capability semantics.

- [x] E1: failed-first evidence covers HTTP, typed client, CLI and renderer boundaries
  CHECK: node /var/tmp/pr-5769-public-errors-79.BBRYzt/verify.mjs red
  EXPECT: FAILED-FIRST VERIFIED
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=efde7038e36f/43 entries; output=FAILED-FIRST VERIFIED

- [x] E2: HTTP persistence/redaction and CLI compatibility pass repeated race checks
  CHECK: node /var/tmp/pr-5769-public-errors-79.BBRYzt/verify.mjs backend
  EXPECT: BACKEND VERIFIED
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=efde7038e36f/43 entries; output=BACKEND VERIFIED

- [x] E3: typed clients and localized renderer preserve safe codes and old-server behavior
  CHECK: node /var/tmp/pr-5769-public-errors-79.BBRYzt/verify.mjs frontend
  EXPECT: FRONTEND VERIFIED
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=efde7038e36f/43 entries; output=FRONTEND VERIFIED

- [x] E4: midpoint self-review finds no unresolved issue in projection or capability semantics
  EVIDENCE: PUBLIC-ERRORS-REVIEW.md records exact allowlisting, terminal suppression, compatibility parity, actual-router checks and immutable client normalization. First green was followed by additional schema/router/cache controls; the final full affected suites pass. No execution or capability changes.

- [x] E5: final source seal, generated drift and every prior preservation manifest match
  CHECK: node /var/tmp/pr-5769-public-errors-79.BBRYzt/verify.mjs seal
  EXPECT: EXACT SEAL VERIFIED
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=efde7038e36f/43 entries; output=EXACT SEAL VERIFIED

- [x] E6: real isolated desktop evidence shows the current diagnostic labels without exposing private state
  EVIDENCE: /var/tmp/pr-5769-public-errors-79.BBRYzt/desktop-before-final.log and desktop-cancellation-final.log exit 0. Real isolated Electron with production daemon/catalog and explicitly synthetic SQLite fixtures: restart retained codes, cancellation returned 202 and cleared its code with native revision 1 unchanged, removal remained recovery-required. Three inspected screenshots and a 2.04-second direct recording are under docs/screenshots/pr-5769-public-errors/. Capture-harness status/navigation failures are retained and explained; no live-provider acceptance claimed.
