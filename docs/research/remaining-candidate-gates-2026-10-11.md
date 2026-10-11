# Remaining candidate gates and batch handoff — 2026-10-11

Ubuntu VPS only. Routine suites ran only in GitHub Actions. No Mac operations,
account signup, paid upgrade, production daemon/session changes, merge, release
or deployment. Earlier failing attempts and immutable published images remain
intact. This report records holds without speculative product registration.

| Candidate | Result / separate PR | Concrete limit |
| --- | --- | --- |
| Docker Agent | [6540](https://github.com/OrchestratorInc/agent-orchestrator/pull/6540), complete Chat-only | Native ACP and actual AO Chat/Electron evidence; no TUI/handoff/reviewer claim. |
| Mastra Code | [6541](https://github.com/OrchestratorInc/agent-orchestrator/pull/6541), complete TUI | Native plugins, exact restore and actual AO TUI evidence; separate retained failures. |
| CodeBuddy Code | [6546](https://github.com/OrchestratorInc/agent-orchestrator/pull/6546), complete TUI | Native Default permissions only, foreground; actual AO restore/permission screenshots. |
| Stakpak | [6550](https://github.com/OrchestratorInc/agent-orchestrator/pull/6550), draft/unregistered | Released native Escape left executing descendants and a delayed side effect; restore duplicated standing messages. |
| Pool | [6551](https://github.com/OrchestratorInc/agent-orchestrator/pull/6551), draft/unregistered | Available authorized Z.ai base is misjoined to `/v4/v1/chat/completions`; native first turn fails while direct `/v4/chat/completions` succeeds. No authorized working alternate credential. |
| Freebuff / Codebuff | [6553](https://github.com/OrchestratorInc/agent-orchestrator/pull/6553), draft/unregistered | Fresh ordinary task failed independent private nested instruction receipt despite file discovery. Native edit/follow-up/cancel controls passed separately. |
| Blackbox CLI v2 | blocked; no registration PR | Official installer still identifies `blackbox-cli-v2`; release metadata linux-x64 HTTP404. Cannot pin the documented ACP product, additive instructions or authorized auth. Unrelated npm package is not substituted. |
| Warp Agent CLI | blocked; no registration PR | No per-session additive private instruction route in current CLI flags/config docs; root/global rule changes would alter user rules. No Warp API key/login established. Existing `.warp` directory holds an Oz remote-server executable, not credential proof. |
| Rovo Dev CLI | blocked; no registration PR | ACLI formula still1.3.39-stable; requires enabled Atlassian site, scoped token, credit allocation. Those are unavailable; provided model keys do not substitute. Separate engine pin/lifecycle remain unproved. |
| Hermes ACP | blocked; existing TUI [6536](https://github.com/OrchestratorInc/agent-orchestrator/pull/6536) preserved | Latest official release remains0.21.6; ACP `_make_agent` kwargs omit ephemeral_system_prompt; new/load extras cannot supply it. Config location is HERMES_HOME/config.yaml, not a supported per-process config-only override. A whole-home overlay has not qualified auth/memory/skills/defaults/history preservation. ACP load is source=acp only, not TUI handoff. |
| Polygraph | excluded from harness registration | Meta-orchestrator over existing agents; a connector is future separate scope. |

Prior completed stages have result records with native/AO proofs, serving source
pins, PNG hashes/download verification, read-back PR comments and exact-head CI.
See [Docker Agent](docker-agent-acp-2026-10-11.md),
[Mastra](mastra-tui-2026-10-11.md), [CodeBuddy](codebuddy-tui-2026-10-11.md),
[Stakpak hold](stakpak-tui-hold-2026-10-11.md),
[Pool hold](pool-acp-hold-2026-10-11.md) and
[Freebuff hold](freebuff-native-hold-2026-10-11.md).
None of the hold candidates claims an actual AO screenshot; screenshot unavailable
because prerequisite qualification prevented an AO launch, not silently passed.

## Refreshed official sources

Fetched2026-10-11 UTC, without executing installers or changing native profiles:

- [Blackbox installer](https://blackbox.ai/install.sh) derives
  `https://releases.blackbox.ai/api/v0/latest?product=blackbox-cli-v2&platform=linux-x64`;
  exact response HTTP404. [Documented ACP](https://docs.blackbox.ai/features/blackbox-cli/acp-integration).
- [Warp CLI reference](https://docs.warp.dev/agents/cli/reference/) documents
  WARP_API_KEY/browser login. [Configuration](https://docs.warp.dev/agents/cli/configuration/)
  still uses repository AGENTS.md/WARP.md and global ~/.agents/AGENTS.md.
  Resume/auto-approve/provider-key flags do not supply private session rules.
- [Atlassian install prerequisites](https://support.atlassian.com/rovo/docs/install-and-run-rovo-dev-cli-on-your-device/)
  explicitly require activated site, credits and scoped token.
  [Pinned current formula](https://github.com/atlassian/homebrew-acli/blob/main/Formula/acli.rb).
- [Hermes0.21.6 ACP construction](https://github.com/NousResearch/hermes-agent/blob/v0.21.6/acp_adapter/session.py)
  and [config-path source](https://github.com/NousResearch/hermes-agent/blob/v0.21.6/hermes_cli/config.py).
  HERMES_CONFIG/HERMES_CONFIG_PATH occur in reserved-variable validation, not
  get_config_path; their names alone are not an implemented override.
- [Freebuff released artifact](https://github.com/CodebuffAI/codebuff-community/releases/tag/freebuff-v0.2.26)
  and [public source snapshot](https://github.com/CodebuffAI/freebuff/tree/76bd2de6e409bbff627d9276eddd39362bbed642).
  Public/private/released source relationship is limited; actual binary proof controls the verdict.

Current main refreshed to a7860405e4d48333204b6627ec3e83b20a99cc00. All-state
candidate alias searches found no additional active Blackbox/Warp/Rovo/Hermes ACP
integration PR. Codebuff historical1826 is closed/unmerged and not duplicated.
No new migration was allocated for unregistered holds. Existing independent
registration/migration branches are preserved without sibling imports.

## Resource and ownership cleanup

Freebuff native-owned TUI/broker child processes were terminated using recorded
PID/kernel identity, and all recorded descendants were absent afterward. Scratch
profiles, history, source worktrees and prior evidence are preserved. No AO state
was created for the failed qualification. Protected old audit PID625660 remains.
Only inactive regenerable Go build cache/executables and archives with separately
preserved released binaries were reclaimed; receipts list paths/hashes privately.
Corepack ~/.cache/node, credentials, UV archives, shared Git and production state
were untouched. Native provider processes no longer remain from this stage.

Next actions: review complete integration PRs independently; keep holds draft.
Requalify explicit upstream/auth prerequisites with fresh native and actual AO
proof before any registration. Never infer runtime success from green unit CI.

Pool alternate provider follow-up: presence-only inspection found no DeepSeek key
in the process environment, OpenCode auth (only zai-coding-plan), or50 native/
audit credential/config files across previous/current profiles, ~/.hermes and
~/.config. Audit helper searches found no configured DeepSeek route. No secret
value was printed/imported and no proxy or native patch was introduced. The
existing Z.ai native route failure remains the honest available-provider block.
