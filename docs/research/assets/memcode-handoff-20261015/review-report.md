# memcode PR6515 VPS preparation, 2026-10-15

Status: **incomplete / AO UI evidence BLOCKED**. Three local source files changed; nothing committed, pushed, or published. No changes to skill PR6509. A fresh native-only independent instruction control passed sampled fresh/restored effects; it does not resolve the historical registered-AO failure or qualify edited source.

## Source and reviewed state

PR https://github.com/OrchestratorInc/agent-orchestrator/pull/6515 was read at head `a12593d9c824cad8ecde05dbbd1680a9dad06d43`. Owned checkout started clean and remains at that detached head with the three intentional working changes. Read-only main reference: `54b28374912cb2771f17b8cc32a9482e6d607bce`; merge base `44acd07e15a4f33bf5dcf5c5c5e7d752325919c4`. Current GitHub snapshot reports CONFLICTING and 23/23 listed checks SUCCESS. These are existing head checks, not checks of the local edits. Raw body/comments/reviews are in `../pr6515.json`; inline reviews and check snapshot are beside this report.

Fetched skill PR6509 head `5af6c2306250c7b2ada2563af5247e2d2c7eddf0` read-only and archived its skill directory to `../skill-reference/`, outside source. Read SKILL, integration-path, evidence-and-handoff, audit-journeys and local-cli-contract. Read repository AGENTS, architecture/status, desktop-dev and PR-description guidance. User execution/publication restrictions override skill workflow commands. No routine suites, unit tests, lint, formatting, typechecks or CI emulation ran.

## Necessary local changes

- `backend/internal/adapters/agent/memcode/restore.go`: current head's last-16-line panic rejection still rejects a panic discussed immediately before a healthy idle composer. Scope detection after the newest idle marker; require composer before footer and reject a later crash or active spinner. This is source reasoning, **not runtime-tested edited code**.
- `restore_test.go`: add nearby historical panic and later crash/spinner regression cases. **NOT_RUN**.
- `frontend/src/docs/content/plugins/agents/index.mdx`: correct stale model-override rejection wording to describe existing native catalog `/model` confirmation and custom endpoint restrictions. Explain exact-preserving chunk boundary limitations.

Already fixed at head, not duplicated: nonzero-exit Unauthorized handling, metadata-independent transcript message hashing, walking back past indented chunk boundaries, partial hook coverage, native model surfaces, generation-aware post-restore identity hooks, and ignored native history. See `source-review.json`. No silent fresh fallback was added. No API, generated artifact, migration, registry or skill changes.

Native `runSessionHooks` trims every nonempty output and joins outputs with two newlines; each output is capped at 8192 bytes. An indivisible oversized paragraph has no exact-preserving split through this interface. Existing code walks backward over safe boundaries and rejects unsupported text instead of truncating or silently changing whitespace. No broader instruction-file replacement, encoding trick or changed native binary was introduced.

Read-only merge-tree analysis found conflicts in `backend/internal/adapters/agent/modelcatalog/catalog_test.go`, `backend/internal/service/agent/service.go`, `backend/internal/storage/sqlite/migrate_burned_versions_test.go`, and `frontend/src/renderer/components/AgentModelPicker.tsx`. Full diagnostic remains `main-conflict-analysis.private.txt`. No merge/rebase was applied, so runtime/source provenance stays explicit. Final publisher must reconcile these in this owned checkout and qualify the resulting head.

## Completion matrix

| Area | Supported scope | Verification | Proof or reason |
|---|---|---|---|
| Source review/fixes | Existing memcode TUI PR only | REVIEWED / edited code NOT_RUN | `source-review.json`, `source-changes.patch` |
| Install | Official script on Linux/macOS; manual native release on other OS per source | UI NOT_RUN | Installer source inspected; existing binary retained; no reinstall or downloads |
| Auth | Existing authorized Z.ai custom endpoint | Native provider calls succeeded; AO probe NOT_RUN | Fresh/restored real turns; credentials never logged/copied to report; configured-only AO status remains unverified |
| Models | Native catalog via `/model`; custom endpoints retain native selection | SOURCE_REVIEW; native configured GLM footer observed | Model selection UI and AO confirmation handshake NOT_RUN |
| Fresh private/project instructions | Additive native session-start output and MEMCODE.md | Native-only sampled effects PASS | `../attempt-native-001/receipt.json`; ordinary task names no private token/path |
| Restored new private instructions | Exact native `/resume` on pinned binary | Native-only sampled effects PASS; current AO NOT_RUN | New private token replaced old; project and user hook effects preserved; same ID/history before continuation |
| Historical restored instruction failure | Registered AO at `a3a4ef82` | FAIL remains | Original `zcode-cortex-20261012/reports/prompt-audit/memcode-focused-003/receipt.json`; configured NEW chunks with OLD witness retained |
| Initial task | One native submission; original file preserved on continuation | Native-only observed | Task list and native history; no claim of UI-created task or executing-write count |
| Native defaults/user rules | Native binary unmodified, user hook/project files preserved | Effects observed; authenticated wire role UNKNOWN | No native system prompt replacement; no provider-wire capture |
| Permission modes | Ask/Auto/allow-all mapped; accept-edits unsupported; catastrophic checks remain | SOURCE_REVIEW; native run used allow-all only | No manual/Auto or full security-isolation qualification |
| Activity | Pre-tool hook partial plus terminal derivation | Already marked partial; edited composer NOT_RUN | `activitydispatch/dispatch.go`, local patch |
| Follow-up | Native ordinary continuation | Native-only observed | Same native ID/workspace and SECOND file |
| Executing-turn cancel / active AO Kill isolation | Separate required gates | NOT_RUN this stage | Historical evidence is historical; no new executing-child/no-late-effect pass |
| Exact restore safety | Full ID, bounded confined history, fail closed | SOURCE_REVIEW and native ID/messages equality | No missing-history AO negative control this stage; no automatic fallback |
| AO UI lifecycle / archive / Kanban / reconnect | Actual Electron required | BLOCKED / NOT_RUN | Complete checkpoint index retained; no screenshots fabricated |
| Chat / handoff / reviewer / switching | Unsupported limited scope | N/A by current registry/capability source | No Chat or cross-interface claim |
| CI | Existing remote head only | 23 listed SUCCESS; local edited source NOT_RUN | `github-checks.json`; no CI triggered |
| OS | Linux x86-64 | Native-only observed | Windows/macOS NOT_RUN |
| Cleanup | Exact native attempt processes | Observed identities gone; protected daemons present | `cleanup.json`; profiles/evidence retained |

## Native attempt provenance and reproduction

`../attempt-native-001/` is a new attempt. It ran only released memcode 0.38.1, upstream `afc80cf1718eaaf2b5abd9c6a0b753ba63f3d186`, binary SHA256 `89cd4375fce660d6c365163745ef83d78e76ad36e803fe4cebb952644fe4ddff`. No AO daemon/runner or UI binary served this attempt. It is **not current-head AO runtime evidence**. The helper snapshot, SHA256 manifest, receipt, private native terminal text, native history and isolated profile survive locally. Terminal text is not a screenshot.

Sanitized command (fresh equivalent attempt requires a new directory/socket, not rerunning over existing receipts):

```sh
python3 /home/azureuser/.ao/audits/harness-handoffs-20261015/memcode/tools/native-independent-001.py
```

The helper acquires `/home/azureuser/.ao/audits/harness-watch-20261010/compute.lock` nonblocking, uses only its unique tmux socket and owned workspace/profile, disables native updates, pins the binary hash, and injects the existing VPS Z.ai credential privately from the same established helper source. It does not dump environment/credential data. Provider/model: Z.ai custom coding endpoint / `glm-5.3-flash`; all-permissions flag `--allow-all` retains native catastrophic-command decisions. Native taskless argv is `memcode run --allow-all`; initialized idle/composer/footer gates input; bracketed-paste sends ordinary tasks. Fresh and continuation waits are bounded at 150 seconds; startup at 35 seconds. No local model/embedding downloads were launched.

First task: `-Please create ordinary.txt containing FIRST exactly once, then finish and wait.` Private hook and project rule independently request other file effects, absent from the task. After `tmux kill-session` returned, generate a new random private token, rewrite the private hook input, launch a fresh taskless TUI, wait ready, and send exactly `/resume <same-full-native-id>`. The new hook's context digest matches updated input; pre-continuation messages equal prior messages. Ordinary continuation: `Please create continuation.txt containing SECOND, then finish and wait.` New private, project, user-hook, initial-file and same-ID checks all returned true. The old private value was absent afterward. Native settlement and these effects were observed; authenticated provider wire roles remain unknown.

Qualification limit: the token was generated after tmux termination returned, but the first native PID was not sampled for reaping at that exact moment. Final observed owned native/tmux/shell identities were all gone after cleanup. This is not a strict executing-child Kill/no-late-effect witness. The stage's native positive does not erase the older AO failure or replace its required registered/UI continuation.

## UI blocker and evidence preservation

The checkpoint index was created before any UI driving (`screenshot-checkpoints.md`). No actual UI was driven or captured. Disk began at 983 MiB free; an existing genuine frontend install in the reference Mastra checkout occupies about 1.3 GiB, more than this stage can allocate while keeping 512 MiB free. This owned checkout has no node_modules. No doomed install, symlink reuse, RAM-backed mount, cleanup of another worker's modules, or UI mock was attempted. Current-head workflow artifact queries returned no retained daemon artifact. A cache-cold daemon build was not attempted under the same reserve; no serving current-head daemon is claimed.

Remedy: provide enough disk-backed capacity for a genuine isolated Electron install plus daemon artifact/build, then capture every index action/result and run fresh registered-AO independent instructions and lifecycle controls on the actual serving edited build. Use unique AO_DATA_DIR, AO_RUN_FILE, AO_DEV_ELECTRON_DIR and loopback port; record PID/executable/hash before/after UI. Do not reuse production or archived native histories. Existing Mastra dependencies and other harness profiles remain untouched.

Original failure receipts and prior screenshots remain in their original directories, never overwritten or represented as new evidence. No screenshots, raw native profiles, credentials, or logs were uploaded. Future drafts are local files only. End cleanup had >512 MiB disk free, no owned observed native identities remaining, and protected PIDs 429811 and 625660 still present. No daemon settings, tunnel, pairing, service or mount changes occurred.
