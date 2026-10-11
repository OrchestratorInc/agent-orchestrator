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
