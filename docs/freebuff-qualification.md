# Freebuff native qualification hold

Freebuff is deliberately unregistered. This package is a guarded candidate, not a
usable AO agent. No domain, registry, database, API, UI, Chat or reviewer entries
are added. Historical Codebuff PR1826 is closed/unmerged; issue6542 remains open.
Codebuff1.0.688 and the SDK are distinct from the tested Freebuff executable.

## Released native proof (Ubuntu VPS, 2026-10-11)

The official Freebuff0.2.26 Linux x64 archive is pinned at
https://github.com/CodebuffAI/codebuff-community/releases/tag/freebuff-v0.2.26.
Archive SHA256: `e5c3b8247d86b7f95f6fbfae0fd8e05dda11bf2dad0e25fa8339d2fe42df4d99`.
Extracted executable SHA256: `e93630048841aaa9564c3ce4b0d804591093ec00471fabcda0668c092d116440`.
Actual `--version` returned0.2.26; `--help` exposes `--cwd` and optional-ID
`--continue`, but no task flag. No upstream binary modifications were made.

Native profiles were isolated below AO audit data with both
`FREEBUFF_CONFIG_DIR` and `FREEBUFF_BYOK_CONFIG_DIR`. The documented connection
metadata schema used an environment reference to an already authorized Z.ai
credential; no key value was persisted in connection metadata or visible input.
No account was created. Native `/byok validate` returned HTTP200 for
`glm-5.3-flash`, explicitly distinguishing reachability from coding qualification.

Passing native controls: a leading-dash task submitted once through the TUI;
correct-workspace native file edit; unchanged root AGENTS.md; follow-up edit;
observed active then settled; executing Python child with PID937267/kernel
start78297970; Escape cancellation, child reaped, no25-second delayed side effect.
These controls do not qualify AO integration or every permission mode.

## Instruction failure retained separately

Hidden `.ao-standing/AGENTS.md` with narrow `.codebuffignore` negation was in
persisted knowledgeFiles, but the native response wrote `standing: null`.
A nonhidden `ao-standing/AGENTS.md` with an AO-owned sibling `.gitignore` and
`.codebuffignore` was read when the task explicitly requested standing context;
that control produced the token and preserved the user's root AGENTS.md.

The decisive independent fresh control instructed the private file to always
write `private-receipt.txt` containing a unique token before each coding task.
The visible task only requested `hello.txt`, never mentioned the receipt, private
file or token. Freebuff created and verified hello.txt, settled, and produced no
private receipt. Discovery/persisted knowledgeFiles and prompted file reading
therefore do not establish private standing instruction consumption independently
of the visible task. Nested rules are insufficient for this admission gate.

The released parser has no append-system/instruction flag. Replacing a bundled
agent definition, modifying root/user instructions, showing standing context as
a user task, or patching native history would not preserve the required native
contract. Exact restore plus renewed private instructions remains unqualified;
public snapshot missing-ID continuation falls back to latest. Restore hard-errors
for every state rather than letting AO replay the task fresh.

## Validation and remaining work

Meaningful test source covers argv safety, invalid/unsupported inputs, canceled
contexts, launch qualification, hook refusal and hard restore failure. Routine
verification runs only in this PR's GitHub Actions; no local suites were run.
No essential AO build was justified before the instruction gate passed.

AO native session identity, input/draft admission, AO Kill/control isolation,
restore, actual Electron screenshot and Chat remain NOT_RUN. AO UI screenshot:
unavailable because no production registration or actual AO session was admitted.
Native-only terminal text is retained privately and is not an AO screenshot.

Required external prerequisite: a released native additive instruction mechanism
that applies independently to ordinary tasks, preserves defaults/user rules and
can be scoped to one AO session. Requalify against that release before enabling
launch, then implement inventory/auth/native model selection, lifecycle boundaries
and UI evidence. This hold intentionally does not close issue6542.

## Recovery diagnosis and upstream proposal (run 2026-10-12)

The remaining prerequisite is now concrete: an explicit additive per-process
instruction input in the released Freebuff CLI. The npm registry still reports
0.2.26. Its private release gitHead is
`962fddbe93c2c7f3278e718e0dc45d92c9920a9c`; the public source snapshot below is
**not** that release's exact source revision.

Independent inspection of the unchanged pinned executable found the root-only
knowledge filter at byte offset 95,885,895. Retained native-005 state SHA256 is
`06678df808cbf132bacd436fddd575c981a589272615575c82b62f01260d5018`.
Its effective system prompt SHA256 is
`94317617bcd00c91c757a5868154a4899ee7e5968d720570c43dcc6bc93e00fd`:
root AGENTS.md content is present; nested private instruction content is absent.
The visible task artifact exists; the independent private receipt does not.
This is deterministic exclusion before inference, not missing discovery or cwd.
No identical provider probe was repeated during recovery.

At public snapshot `76bd2de6e409bbff627d9276eddd39362bbed642`:

- [Prompt formatting](https://github.com/CodebuffAI/freebuff/blob/76bd2de6e409bbff627d9276eddd39362bbed642/packages/agent-runtime/src/templates/strings.ts#L130)
  filters project knowledge to root AGENTS.md/CLAUDE.md; home/user knowledge is
  merged separately without that path filter.
- [Run transport](https://github.com/CodebuffAI/freebuff/blob/76bd2de6e409bbff627d9276eddd39362bbed642/sdk/src/run.ts#L612)
  omits the internal userKnowledgeFiles input in fresh and continuing runs.
- [Native arguments](https://github.com/CodebuffAI/freebuff/blob/76bd2de6e409bbff627d9276eddd39362bbed642/cli/src/cli-args.ts#L52)
  expose no additive instructions or root-agent selection in Freebuff.

Supported alternatives inspected: profile/config-dir isolation does not redirect
home instruction loading; nested discovery remains filtered; on-demand skills
and explicit reads depend on visible task context; custom agent definitions
replace the default root or require explicit selection. None is a supported
private additive session overlay preserving native defaults. Changing HOME,
root/user rules, bundled agents, or the installed binary is outside this contract.

[Unapplied upstream patch proposal](freebuff-upstream-append-instructions.patch)
adds a proposed `FREEBUFF_APPEND_INSTRUCTIONS_FILE` input and bounded UTF-8
loader, then transports a distinct additive host overlay into existing knowledge
formatting. **This environment variable does not exist in stock 0.2.26.** The
proposal targets the immutable public snapshot, was not built or executed, and
was not submitted to an upstream repository. It changes no installed native code.
The file read occurs before client creation and outside its catch-to-null path;
unreadable/invalid input rejects admission. It preserves home/project maps,
default root selection, auth, tool schemas, broker and permissions. The reserved
host key is refreshed on cloned state; cached-client context is immutable until
client replacement. Linux file checks need a separately reviewed cross-platform
implementation before upstream distribution.

Included proposed loader regression tests are unexecuted. Required additional
upstream regression and released-native acceptance criteria:

| Boundary | Required observable result |
| --- | --- |
| Default input absent | Ordinary native behavior and user/project instructions preserved |
| Fresh run plus private file | Effective default-root prompt contains home, root and host context; visible task unchanged |
| Second turn | Host effect remains present; no duplicate overlay or user-map mutation |
| Prior-state continuation | Fresh host overlay replaces stale host context; unrelated knowledge preserved; actual effective prompt refreshed |
| File rejection | Relative/missing/directory/symlink/empty/oversized/invalid UTF-8/NUL input fails before any model/tool turn |
| Native root variants | Qualify actual selected roots; templates omitting knowledge formatting are unsupported, never silently admitted |
| Real ordinary task | Correct-workspace edit plus independently required private artifact, without naming the private path/token/receipt |
| Native cancellation then AO | Executing-child cancellation, independent AO Kill/control isolation, current rendered input guards and actual AO UI PNG |

An official released input plus those native/AO proofs remains required before
registration. Non-restorable TUI scope is still acceptable; restore, Chat,
handoff and reviewer surfaces remain unsupported. AO screenshot is unavailable
because the mandatory instruction prerequisite prevents truthful AO admission.
Existing launch and restore gates remain intact. No local routine suite ran.
