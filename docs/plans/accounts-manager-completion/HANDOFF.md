# Session 81 continuation acceptance

Compared against its preserved worktree, report, and `/tmp/accounts-manager-81/final-audit.json` on 2026-09-27. All ten continuation files were accepted after comparison. No inherited vault or Serve files, dependency trees, or peer ledgers were imported.

| File | Decision and current integration |
| --- | --- |
| `accounts-manager/UPSTREAM.md` | Accepted unchanged: records the narrow approved extension. |
| `accounts-manager/engine/sdk/auth/interfaces.go` | Accepted unchanged: explicit listener ownership and URL delivery. |
| `accounts-manager/engine/sdk/auth/codex.go` | Accepted unchanged: uses the caller-owned listener, preserves provider exchange. |
| `accounts-manager/engine/sdk/auth/claude.go` | Accepted unchanged: same callback boundary, removes secret-bearing callback logging. |
| `accounts-manager/engine/sdk/auth/listener_callback.go` | Accepted unchanged: loopback and exact redirect validation, bounded callback lifecycle. |
| `accounts-manager/engine/sdk/auth/listener_callback_test.go` | Accepted, then corrected the asynchronous browser fixture's context lifetime after reproducing a race. |
| `accounts-manager/runner/internal/runner/credential_runtime.go` | Accepted, then extended with model registration for the live Serve integration. |
| `accounts-manager/runner/internal/runner/credential_runtime_test.go` | Accepted, then extended for integrated admission behavior. |
| `accounts-manager/runner/internal/runner/credential_browser_test.go` | Accepted unchanged: synthetic browser lifecycle coverage. |
| `accounts-manager/runner/internal/runner/credential_vault_admission.go` | Accepted, then strengthened to compare proxy and prefix settings. |

Rejected imports: the inherited vault/Serve files and whole ledgers, because session 79 owns newer integration work. The report's unwired-Serve statement is accurate for the peer snapshot, not the current tree. Its desktop walkthrough covers inherited UI only. Platform evidence is compile-only. The existing full-engine media failure remains a release gap, not a reason to modify unrelated engine code.

Current checks: integrated runner build/vet/full race passed; daemon management adapter race passed; 100 callback race repetitions and the full SDK authentication suite passed. No live provider credential was used. No remote mutation or evidence publication occurred.
