# Letta Code prototype withdrawn

PR #6492 is withdrawn after independent source review. Letta Code v0.34.9
(commit `cbf9026030c74e60432c246aec2bb0ec08fd49bc`) does not provide the private
instruction and lifecycle boundaries this prototype claimed. This branch must
not be merged as a production integration.

Four release-specific blockers were found:

1. **Private context can become visible.** UserPromptSubmit stdout is accumulated
   across hooks. If a later hook blocks, the TUI prints all accumulated feedback,
   including the earlier AO instructions. `quiet: true` suppresses executor
   logging, not this transcript path. See `src/hooks/executor.ts:429` and
   `src/cli/app/use-submit-handler.ts:662` in the pinned source.
2. **Prompt acceptance is premature.** UserPromptSubmit runs before subsequent
   blocking hooks and the pending-approval guard. It cannot acknowledge that an
   AO message reached the model. See `src/cli/app/use-submit-handler.ts:652-712`.
3. **Stop does not establish idle.** Another Stop hook can veto completion, and a
   later turn_end mod can request continuation. See
   `src/cli/app/use-conversation-loop.ts:1490-1582`.
4. **Permission activity cannot be reliably cleared.** PermissionRequest omits
   tool_call_id; native permission checking happens before PreToolUse for client
   tools. AO cannot reliably correlate the later tool observation with the
   blocked request. See `src/hooks/index.ts:179` and `src/tools/manager.ts:2192`.

Unsafe raw instruction output and the unsupported submit, semantic-acceptance,
permission-blocked and Stop-idle signals were removed before closing the PR.
The remaining branch is an unmerged prototype, not a supported harness.

Native mods were considered as an alternative. The inspected release loads
mods from global/agent directories; overriding LETTA_MODS_DIR replaces native
user-global discovery. turn_start remains cancellable by later mods, turn_end
still permits continuations, and llm_start exists only on the local backend.
No bounded implementation preserving native defaults and covering local plus
Cloud backends was established. A later integration needs a proven replacement
contract rather than re-enabling these hooks.

[Inspected upstream source](https://github.com/letta-ai/letta-code/tree/cbf9026030c74e60432c246aec2bb0ec08fd49bc).

Remote CI on the first revision found a missing exported-constant comment and
an outdated install-plan count; both source issues were corrected. Local tests,
builds, lint, typechecks, installs and provider probes were not run at the user's
request. Live lifecycle, authorization and private-context conformance remain
**NOT_RUN**. No CI result substitutes for those live gates.
