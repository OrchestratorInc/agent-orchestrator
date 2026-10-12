# Freebuff PR #6553 recovery diagnosis — 2026-10-12

This is a read-only diagnosis of the shipped Freebuff 0.2.26 native TUI and existing negative controls. No source edits, installs, builds, tests, native/provider turns, process changes, or publication were performed. All inspection ran on the authorized VPS. This report is the only new file written.

## Finding

The private instruction failure is deterministic prompt construction, not a provider failure, missing file, incorrect cwd, or merely model disobedience. Freebuff discovers and persists nested AGENTS.md files, but its system-prompt interpolation deliberately accepts only project knowledge keys equal to `agents.md` or `claude.md`. A key such as `ao-standing/AGENTS.md` is excluded.

The remaining current-release problem has no supported AO-only fix found within the required boundaries. Freebuff 0.2.26 does not expose an additive instruction file/env option, does not expose Codebuff's `--agent` flag, and does not pass the internal unfiltered per-session knowledge map through its public run configuration. Do not repeat the same nested-file provider probe. A real supported native additive route, or an upstream source change subsequently released and qualified, is the next dependency. TUI-only/non-restorable scope removes the restore requirement; it does not remove the standing-instruction failure.

## Evidence and provenance

- Existing integration draft: https://github.com/OrchestratorInc/agent-orchestrator/pull/6553, recorded head `9fbb97e559c245633554ee6545836af03f7348d7`; unregistered and launch-gated.
- Existing evidence root: `/home/azureuser/.ao/audits/candidate-integrations-20261011/reports/codebuff`.
- Native executable: `/home/azureuser/.ao/audits/candidate-integrations-20261011/tools/freebuff/freebuff`.
- Binary SHA-256 recomputed read-only during this diagnosis: `e93630048841aaa9564c3ce4b0d804591093ec00471fabcda0668c092d116440`, matching `release.json`.
- Existing archive SHA-256: `e5c3b8247d86b7f95f6fbfae0fd8e05dda11bf2dad0e25fa8339d2fe42df4d99`; archive had already been removed.
- Public source snapshot: `76bd2de6e409bbff627d9276eddd39362bbed642` under CodebuffAI/freebuff. Source paths and line numbers below refer to this immutable snapshot.
- npm registry read during diagnosis still reports only latest `0.2.26`, published `2026-10-08T23:17:41.834Z`; its wrapper metadata names private repository `CodebuffAI/freebuff-private` and gitHead `962fddbe93c2c7f3278e718e0dc45d92c9920a9c`. The public snapshot must not be claimed as the release's exact source revision. The critical filter and native parser were independently corroborated in the actual pinned binary, using read-only memory mapping, without executing it.
- Public GitHub releases/latest and guessed freebuff-v0.2.26 API endpoints returned 404. No new downloadable released fix was established. This is not proof that no unpublished/private change exists.
- Applied contributor guidance: `/home/azureuser/.ao/audits/harness-watch-20261010/skill/SKILL.md`, `references/integration-path.md`, and `references/evidence-and-handoff.md`. Their nested-discovery warning is now explained by the native code below.

## Exact instruction pipeline

1. `sdk/src/run-state.ts:778–800` selects one AGENTS.md/CLAUDE.md per directory. `loadKnowledgeFilesFromPaths` at 821–844 reads selected paths into `knowledgeFiles`, including nested directories. Fresh initialization at 899–915 discovers them.
2. `packages/agent-runtime/src/templates/strings.ts:130–148` expands `KNOWLEDGE_FILES_CONTENTS`:

```ts
Object.entries(fileContext.knowledgeFiles)
  .filter(([filePath]) => {
    const lowerPath = filePath.toLowerCase()
    // Root-level knowledge files only (AGENTS.md, CLAUDE.md)
    return KNOWLEDGE_FILE_NAMES_LOWERCASE.includes(lowerPath)
  })
```

The code then spreads `fileContext.userKnowledgeFiles` into the map without the root-path filter. `common/src/constants/knowledge.ts:3–6,17–19` defines the two canonical names. This distinguishes discovery from system-prompt delivery.

3. The 0.2.26 binary contains the equivalent expression at byte offset **95,885,820**:

```js
[Gt.KNOWLEDGE_FILES_CONTENTS]:()=> {
  let u=Object.entries({
    ...Object.fromEntries(Object.entries(A.knowledgeFiles)
      .filter(([B])=>{let C=B.toLowerCase();return R3.includes(C)})
      .map(([B,C])=>[B,C.trim()])),
    ...A.userKnowledgeFiles
  })
  // fenced blocks and Project instructions header follow
}
```

The binary canonical names occur at byte offset **95,884,111**; its header appears at **95,883,952**. These offsets are for the binary SHA above, not line numbers in source.

4. `agents/base3.ts:69–89` places the knowledge placeholder in the ordinary native system prompt. `agents/base2/base2.ts:256,368–370` does likewise for the other family. `packages/agent-runtime/src/run-agent-step.ts:1127–1151,1249–1254` formats the native system prompt, preserves parent-system behavior where applicable, and saves the effective result to `initialAgentState.systemPrompt`.

## The retained state proves the exclusion

The existing `native-005` run state is:

`reports/codebuff/native-005/profile/projects/workspace/chats/2026-10-11T16-47-52.437Z/run-state.json`

Read-only observations:

| Field/effect | Observation |
| --- | --- |
| File SHA-256 | `06678df808cbf132bacd436fddd575c981a589272615575c82b62f01260d5018` |
| Effective systemPrompt SHA-256 | `94317617bcd00c91c757a5868154a4899ee7e5968d720570c43dcc6bc93e00fd` |
| projectRoot and cwd | Both equal the native-005 workspace |
| knowledgeFiles keys | `ao-standing/AGENTS.md`, `AGENTS.md` |
| userKnowledgeFiles keys | Empty |
| Selected native root | `base3-free-mimo` |
| Effective selected template model / inference model | `glm-5.3-flash`, BYOK |
| Root project audit token in systemPrompt | Present |
| Private standing audit token in systemPrompt | Absent |
| Private nested path in systemPrompt | Present as file-tree/discovery text, not its mandatory instruction content |
| Visible task | `-Create hello.txt in this workspace containing hello. Use native file editing.` |
| User-visible requested artifact | `hello.txt` exists |
| Privately required artifact | `private-receipt.txt` absent |

The effective system prompt's project-instructions block contains only the root AGENTS.md text. The same project-present/private-absent system-prompt pattern occurs in retained native-003 and native-004 states.

The successful native-004 control asked for a proof field using private standing context and asked to read project instructions. Its native message history records a `read_files` tool call for `AGENTS.md` and `ao-standing/AGENTS.md`, followed by a tool result containing the private token. The control therefore demonstrates an ordinary file read triggered by task context. It does not demonstrate unconditional standing instruction delivery. The private token was absent from its effective system prompt too.

The existing qualification report's `nativeControls.gates.standing: true` is subordinate to this explicit-control limitation; do not promote that field to a passing independent standing-instruction gate.

## Agent definitions, initial agent, and native configuration

### Root selection and user agents

- `cli/src/cli-args.ts:52–73`: Freebuff's parser supports version/help, `--continue [conversation-id]`, `--cwd`, `--trust-agents`, and login. The comment explicitly says no prompt arguments or agent override. Codebuff's separate branch at 75–109 supports `--agent`, modes, and prompt arguments. The actual 0.2.26 binary contains the same split around byte offsets **100,317,325–100,318,800**.
- `cli/src/state/chat-store.ts:218,306–315`: Freebuff starts in LITE and ignores mode changes.
- `cli/src/utils/freebuff-agent-selection.ts:27–49`: the chosen model/catalog selection determines the Freebuff root. `cli/src/utils/constants.ts:135` selects the base3 family in this snapshot.
- `cli/src/hooks/use-send-message.ts:116–128,642–648`: an explicit agent, if one exists, wins; otherwise mode/model selection wins. Loading an arbitrary local agent does not make it the initial root.
- `sdk/src/run.ts:646–665`: BYOK rewrites the model and context limits on local templates; it does not add private instructions. This explains native-005's root ID `base3-free-mimo` combined with model `glm-5.3-flash`. The ID does not prove Mimo inference was used.
- `sdk/src/agents/load-agents.ts:158–167,248–298`: default locations are cwd/.agents, parent/.agents, and homedir/.agents. Later directories overwrite the same ID; **home wins in the actual iteration**. The opposite prose claim in `local-agent-registry.ts:56` is stale and must not drive an implementation.
- `cli/src/index.tsx:321–345` gates repository agent directories for trust and initializes the registry; `cli/src/utils/local-agent-registry.ts:321–374` replaces bundled definitions on matching IDs and makes distinct user agents spawnable by eligible roots.
- Bundled definitions are compiled into the binary (`local-agent-registry.ts:172–177`). There is no documented native exported append-to-bundled-root hook here. A same-ID local definition is a full replacement, not an additive instruction extension; replacing it with copied source would violate the native-defaults constraint and be tied to a different source snapshot.
- `common/src/templates/initial-agents-dir/types/agent-definition.ts:237–250` supports `systemPrompt`, `instructionsPrompt`, and `stepPrompt` on an agent definition. These shape that agent, not an external session-wide overlay. The native runtime inserts instructionsPrompt with INSTRUCTIONS_PROMPT tags after user input (`run-agent-step.ts:1216–1223`).
- `INITIAL_AGENT_PROMPT` is a formatter placeholder for an internal `intitialAgentPrompt` argument (`templates/strings.ts:43–68,127–129`). It is not a CLI flag, environment variable, initial-agent selection setting, or per-session append API.

### Why the apparently promising internal map is not usable from this CLI

- `sdk/src/run-state.ts:119–122,985–990` accepts internal `userKnowledgeFiles` and merges it with discovered home instructions. This map reaches the unfiltered placeholder branch.
- The public `CodebuffClientOptions` fields in `sdk/src/run.ts:199–208` expose `knowledgeFiles` and `agentDefinitions`, not `userKnowledgeFiles`. More decisively, `runOnce`'s fresh initialization at 625–640 and previous-run override at 612–623 omit `userKnowledgeFiles`. Passing an invented property would not reach initialization.
- `cli/src/utils/codebuff-client.ts:85–108` constructs the SDK client with cwd, bundled/local definitions, native tools, etc., but no per-session instructions. `cli/src/utils/create-run-config.ts:95–131` likewise does not provide knowledge or instruction input.
- `generateInitialRunState` has a userKnowledgeFiles parameter internally (`sdk/src/run-state.ts:1045–1077`), but the Freebuff CLI does not call that external host interface. Building a separate SDK host would be a distinct product/integration, not proof for the released Freebuff TUI. Preseeding a persisted run-state file is not a supported initialization mechanism.

### Cwd/profile boundaries

- `cli/src/init/init-app.ts:9–14` calls process.chdir(--cwd) and makes that directory projectRoot. Retained failed state confirms the intended workspace. Changing launch cwd to the private nested directory would alter the tool/project root and demote real project instructions to an unrelated ancestor; it is not a private-instruction fix.
- `sdk/src/run-state.ts:715–770` reads hidden `~/.AGENTS.md` or fallback `~/.CLAUDE.md` from `os.homedir()`, case-insensitively, choosing the first priority. Its optional homeDir parameter is documented for testing and is not supplied by the CLI.
- `FREEBUFF_CONFIG_DIR` changes the CLI settings/history/profile directory only (`cli/src/utils/config-dir.ts:16–35`). It does not redirect `os.homedir()` or user knowledge discovery. `FREEBUFF_BYOK_CONFIG_DIR` separately isolates BYOK storage. They are useful profile isolation parameters, not instruction selectors.
- `cli/src/utils/settings.ts:45–93,146–284` uses an allowlist of settings including model/BYOK selections; there is no initialAgent/systemPrompt/instructions field. Arbitrary JSON keys are discarded on load.
- The home loader is independently present in the release binary near byte offset **96,244,375**; the initialization merge `h={...await Ng$({fs:J,logger:P}),...U}` is near **96,246,150–96,246,670**. This proves an internal mechanism exists, not that an external configuration switch exists.
- Modifying the real user hidden rules, installing root project AGENTS/CLAUDE content, altering root ignore/rule files, replacing native bundled agents, synthesizing visible instructions, or changing HOME to disguise host context is outside this recovery's allowed approach.

## Ranked recovery hypotheses

### 1. Upstream explicit additive per-session instruction support — sound source proposal, not currently supported

The smallest coherent native change would expose an explicit absolute `--append-instructions-file` (name illustrative, **not a current flag**) or equivalent dedicated process environment path. Read that bounded private file from AO-owned session data before admitting the first turn. Preserve native root selection, tools, project files, actual user rules, auth and provider selection.

A minimal reuse of the existing internal mechanism would:
1. Add the CLI input and transport it via `codebuff-client.ts` or `create-run-config.ts`.
2. Add an explicit host instruction field to the SDK options and forward it through both fresh initialization and subsequent-turn overrides in `sdk/src/run.ts`.
3. Merge that content under a distinct host/session key alongside existing userKnowledgeFiles in `run-state.ts`, without replacing home or project maps. Prefer a separate hostInstructions field if stable provenance/removal across turns is needed.
4. Preserve the bundled prompt and current root knowledge behavior in `templates/strings.ts`; do **not** broadly include all nested AGENTS files as global rules.
5. Make read/validation failures fail launch/turn admission rather than silently dropping AO instructions. Bound content; retain full valid content without truncation. Do not expose it as the visible initial task.

Native source, release, and proof prerequisites:
- Obtain a genuine released native implementation with a documented input; a local modified binary is not the pinned official 0.2.26 contract.
- Verify default root/user instruction preservation and actual effective system-prompt inclusion before the first ordinary task; confirm the same on a second turn.
- Prove a fresh task that never names the private file/token/receipt creates its independently required artifact in the AO worktree.
- Keep the original native-005 negative evidence separate.
- Qualify any bundled/custom agent variants actually admitted. User definitions that omit the knowledge placeholder or inherit a different parent prompt cannot be assumed to consume this route.
- Routine suites belong in CI under the user's policy. Only after a supported route exists should the implementer spend authorized native/AO proof and screenshot work.

This is a concrete patch proposal and release dependency, not permission to patch the installed binary, monkeypatch module internals, or claim current Freebuff supports the flag.

### 2. A newer official release — possible future route, none established now

The registry still points to 0.2.26. Reinspect real later release metadata and the same initialization boundary when one is published. Do not assume public main's SDK capabilities have shipped in the CLI.

### 3. Separate Codebuff SDK adapter — technically distinct scope

A dedicated SDK host can control agent definitions and initial session state, but it does not establish the Freebuff native TUI's capabilities. Do not silently pivot PR #6553 into Codebuff, a custom SDK terminal, Chat, or a published agent. Those require their own product identity and conformance decision.

## Immediate implementer guidance

Keep the current unconditional restore error, and keep standing-instruction/launch admission gated for 0.2.26. Non-restorable TUI scope is allowed, so exact restore is not the next investigation. The source draft can be clarified with this precise capability diagnosis and upstream feature dependency, but production registration would still be unsupported.

Do not spend another provider turn on: nested AGENTS discovery, .codebuffignore adjustments, visible "read the instructions" nudges, a custom subagent that is never selected, an invented settings key, or replacing the root definition. None changes the proven native filter plus missing CLI transport.

Only when a supported instruction route passes should work advance to native readiness/input, cancellation, AO Kill and isolation, real AO UI capture, registration and current-head CI. Existing successful coding/cancellation controls remain useful historical evidence; AO lifecycle and UI evidence remain NOT_RUN, with screenshot unavailable because the mandatory prerequisite prevented AO admission.

## Useful immutable source links

All line references above use this root:
https://github.com/CodebuffAI/freebuff/tree/76bd2de6e409bbff627d9276eddd39362bbed642

- Filter: https://github.com/CodebuffAI/freebuff/blob/76bd2de6e409bbff627d9276eddd39362bbed642/packages/agent-runtime/src/templates/strings.ts#L130
- Internal knowledge loading/merge: https://github.com/CodebuffAI/freebuff/blob/76bd2de6e409bbff627d9276eddd39362bbed642/sdk/src/run-state.ts#L715
- Missing public run transport: https://github.com/CodebuffAI/freebuff/blob/76bd2de6e409bbff627d9276eddd39362bbed642/sdk/src/run.ts#L612
- Freebuff parser: https://github.com/CodebuffAI/freebuff/blob/76bd2de6e409bbff627d9276eddd39362bbed642/cli/src/cli-args.ts#L52
- CLI root/agent definitions: https://github.com/CodebuffAI/freebuff/blob/76bd2de6e409bbff627d9276eddd39362bbed642/cli/src/utils/local-agent-registry.ts#L321
- Profile config: https://github.com/CodebuffAI/freebuff/blob/76bd2de6e409bbff627d9276eddd39362bbed642/cli/src/utils/config-dir.ts#L16

No state cleanup was necessary for this diagnosis: no native process, daemon, session, profile, credential, or checkout was changed.
