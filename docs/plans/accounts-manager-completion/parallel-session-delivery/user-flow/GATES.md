# Gates: live-session delivery

OWNS: docs/plans/accounts-manager-completion/parallel-session-delivery/user-flow/**

Scope: track the outstanding product outcomes and the bounded external desktop-launch correction. Runtime edits require their existing independent review dependencies; this ledger grants no additional source ownership.

- [x] L1: isolated desktop detects installed provider executables while personal credential paths remain masked
  CHECK: /var/tmp/pr-5769-integrated-79.6t2fcN/node22/bin/node /var/tmp/pr-5769-user-flow-79.nBnXAa/check-launch.mjs
  EXPECT: ISOLATED_LAUNCH_READY
  EVIDENCE: exit=0; shell=/usr/bin/fish; cwd=/home/ghoul/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-79; path=0714becd3eff/40 entries; output=ISOLATED_LAUNCH_READY

- [x] L2: the actual desktop shows the corrected harness state and the existing empty scratch inventory
  EVIDENCE: /var/tmp/pr-5769-user-flow-79.nBnXAa/desktop-check-reload.log exits 0 with REAL_DESKTOP_ACCOUNT_CONTROLS_VISIBLE; harness-fixed.png and accounts-ready.png inspected. Executable is detected and sign-in is correctly absent. Zero accounts/sessions before and after. The first renderer was killed during startup and recovered by reloading the real native renderer; clean cold-launch reliability remains open under V1. This does not prove persistence of an actual credential or provider session.

- [ ] R1: reviewed production containment completes exact deletion and crash recovery without harming replacements or unrelated sessions
  EVIDENCE: pending; five retained positive cases remain failing; unchanged containment candidate awaits independent review

- [ ] A1: managed Chat, isolated credential profiles and explicit setup-token migration/reconnect support the requested session account choices
  EVIDENCE: 2026-09-29 live OAuth Chat on the tested provider completed replies, queue preservation and B-to-A handoff with conversation continuity. Managed Codex Chat, isolated native sign-in profiles and explicit setup-token migration/reconnect remain open. Existing unsupported guards remain in force.

- [ ] E1: real authorized A/B sessions overlap and complete switching, retry/cancel, removal/recovery, revocation, restart, queue and eligible usage checks
  EVIDENCE: 2026-09-29 actual A/B terminal replies overlapped by 1695 ms after explicit supported-model selection. Native desktop terminal and Chat handoffs completed with preserved history. Pre-stop cancellation preserved the committed account; a queued Chat turn completed after cancellation. Temporarily disabling A denied A while B completed; all accounts were re-enabled. Usage returned HTTP 200 for all three, with C already exhausted. Daemon restart preserved saved accounts and binding revisions, but a waiting switch became failed and retry returned 409. Default model/catalog, exhausted-account error classification and hidden usage remain defects. No permanent account removal, runner cold restart or platform certification was attempted. See LIVE-RESULTS.md and the external live-events.jsonl.

- [ ] V1: final integrated suites, native platforms, responsiveness, main integration and independent review pass on one recorded snapshot
  EVIDENCE: pending; native hosts unavailable, full suites retain failures and remote main conflicts remain
