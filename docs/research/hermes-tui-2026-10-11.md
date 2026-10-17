# Hermes: historical AO TUI evidence

Evidence recorded on the authorized VPS on 2026-10-11. These results retain their original source and native versions; this relocation does not retest the current PR head.

Source: `c5615c8494821d572649ae3847977b8aaa4fd4b8`. Routine validation was PR CI only; native/AO runtime proof and screenshots were on the VPS.

Hermes official v0.21.6 source `818c13be1dc4fd28987e1e881a9408224afd4535` uses classic interactive `chat --cli --in <workspace>`. Its native banner dates the version 2026.9.24; release metadata identifies 2026-10-08. Additive per-process `HERMES_EPHEMERAL_SYSTEM_PROMPT` preserves native personality and safety. Modern `--tui` does not qualify for this prompt route. Sticky/named profiles, managed overlays and unresolved environment-expanded configuration fail closed. Native permissions remain native; no upstream internals or global plugins were modified.

Hermes exact restored identity digest: `b420afc756aa8d5c3c5ac71f51286869ff0401f39c16c1f173fcdd200b538581`; restored generation `ecc6f9c2-73ed-4532-9300-807bd29c64bb`. `/new` digest `fb5422827ea64e2d373f5d183aa3314c973f0680e84613767ead4f7fa72ccc31` matches the actual native PTY breadcrumb while the startup lease retains the old digest. Native `/new` rows can persist NULL cwd: live identity is supported through corroborated process/PTY evidence, but those conversations are not restorable under this adapter. Persisted restore requires durable matching workspace/history and fails closed after breadcrumb ownership loss.

## Recorded lifecycle gates

| Gate | Recorded result |
|---|---|
| Model writes real file in actual AO worktree | PASS |
| Initial task exactly once; hidden arithmetic instruction plus AGENTS.md | PASS: 437, PROJECT6509, count 1 |
| Second AO message; active to settled; current empty composer | PASS, count 2 |
| Executing-shell native Ctrl+C, PID/start-time reap, no late file after 32 seconds | PASS |
| Independent AO Kill with unrelated control unchanged | PASS, HTTP 200 |
| Exact native restore after Kill, empty composer, fresh instructions | PASS: same native identity, new generation, 837, count 3 |
| Native permission guard without accepting approval | PASS: current native `/new` modal retained |
| Current `/new` identity replaces stale startup lease | PASS: new breadcrumb identity, same AO generation, count 4 |
| Native shell AO routing, persisted checkpoint and HTTP readback | Not separately qualified in final proof |

## Retained failures and limits

Native attempts 001–005 and AO003 italic-readiness,004 token-refusal,005 dirty-build and006 /new NULL-cwd identity failures remain historical failures.007 is intermediate;008 is the final reported source. Configured auth, missing model catalog, and runner activity/second-message failures prevent a full strict audit PASS. Classic TUI is qualified; modern --tui does not inherit this instruction proof. The incomplete blank screenshot has no verified capture UTC or identity before/after; no later provenance is borrowed.

Final target Kill returned HTTP200; its PTY host and owned daemon882470 were confirmed absent.

Owned Electron processes were stopped; profiles, evidence, binaries and dirty scratch worktrees were preserved. No production service was changed.

## Original screenshots

![hermes-004-incomplete-blank](assets/hermes-openjarvis-20261011/hermes-004-incomplete-blank.png)

![hermes-004-token-refusal](assets/hermes-openjarvis-20261011/hermes-004-token-refusal.png)

![hermes-006-restored](assets/hermes-openjarvis-20261011/hermes-006-restored.png)

![hermes-008-restored](assets/hermes-openjarvis-20261011/hermes-008-restored.png)

Adjacent JSON retains original provenance. [Original combined narrative, preserved for cross-run context](hermes-openjarvis-tui-2026-10-11.md).