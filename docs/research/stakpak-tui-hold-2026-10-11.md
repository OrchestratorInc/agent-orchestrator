# Stakpak TUI qualification hold

Stakpak is **unregistered and unavailable in AO**. This guarded package is a
reviewable starting point, not a shipped integration. Launch, hooks and Restore
hard-error; Restore never falls through to a fresh conversation. No registry,
API enum, database migration, picker, reviewer or ACP capability is added.

## Pinned native proof

Ubuntu x86_64, official Stakpak v0.3.88 release, source
`d3eba6f502a79b993b826ef3ab4065eec9e38dfa`.

- Release archive SHA-256: `1e923d6b632e6bda743b3f48c40914f657853478647c26d0a47729a7728490c6`.
- Executable SHA-256 before/after: `5329d4d44afbc9df57d4c99fd9f066750bce81c73d0e459171e6610640321663`.
- Existing authorized VPS Z.ai custom OpenAI-compatible provider, `zai/glm-5.3-flash`; configured credentials alone remain unknown authorization inventory.
- Native file edit in the disposable workspace consumed independent private
  `--system-prompt-file` instructions and preserved/discovered AGENTS.md.
- Leading-dash input worked when entered into the initialized TUI. A positional
  CLI task was accepted but **did not submit a TUI turn**. Future delivery must
  use a current-viewport, serialized, readiness-gated after-start path.
- `--session UUID` restored actual history/tool state. A follow-up changed the
  existing proof file and consumed a newly supplied standing token.

## Registration blockers

On 2026-10-11 15:57:52–15:58:49 UTC, native executing-shell cancellation failed.
The manually approved `run_command` executed:

```sh
sh -c 'echo $$ > cancel-shell.pid; touch child-start; sleep 25; touch late-side-effect'
```

After the child-start marker, Escape returned the native TUI to its composer.
The shell PID 932367 (kernel start 78005410) and sleep survived, reparented to
PID 1092. The delayed side-effect file appeared. A settled screen was therefore
not process cancellation. The command completed before final owned teardown;
no native/child processes remained in the later ownership scan.

The pinned native implementation spawns `sh -c` and cancellation calls
`child.kill().await` on that immediate child, without process-group cleanup.
The documented `cancel_task` applies to separately created background tasks,
not this foreground `run_command`. Disabling subagents or accepting a narrower
single-process command cannot establish cleanup for ordinary shell descendants.
No native monkeypatch, security disablement or fabricated passing control is used.
This blocks a fresh/non-restorable shell-capable TUI too.

Restore adds supplied system instructions before the already persisted system
messages. The restored checkpoint contained two system messages and two
occurrences of the original standing token, despite successful fresh-token
consumption. Repeated renewal without accumulation is not qualified. Omitting
new instructions would retain stale instructions; editing native checkpoints
would be an unsupported history mutation. Non-restorable scope would avoid
this restore claim but would not resolve the cancellation blocker.

## Supported guarded source

Startup flag preparation validates an absolute workspace, UTF-8 task data and a
bounded, non-symlink regular instruction file; supplied text must match the file.
It never puts task text in startup argv. Model/effort/mode, permissions, tool
restrictions and caller-assigned identities are refused pending qualification.
Native identity is not fabricated from AO metadata. Binary discovery is PATH
only and makes no version claim; auth stays unknown.

ACP remains excluded: inspected native load records an ID without restoring
history, and startup can write nonprotocol stdout. No handoff is advertised.

## Verification and next gate

Focused tests cover instruction admission, source-backed startup flags,
after-start strategy, unsupported inputs, canceled contexts and hard launch/
restore/hook refusal. Routine tests/build/lint run in this draft PR's GitHub
Actions only; no local suites were run. Native proof is not AO proof.

**Actual AO UI screenshot: NOT_RUN / unavailable** because the unsafe harness
was not registered or launched in AO. AO activity, input admission, Kill,
control isolation and current-source Electron proof remain NOT_RUN; no native
terminal text or synthetic image substitutes for them.

Requalify a released native fix with descendant reaping/no-late-side-effects,
then instruction renewal/identity and permission boundaries. Only then wire
production registration and obtain actual AO runtime/screenshots and exact-head
CI. No migration number is consumed by this unregistered package.

Sources: [release](https://github.com/stakpak/agent/releases/tag/v0.3.88),
[native shell/cancellation](https://github.com/stakpak/agent/blob/d3eba6f502a79b993b826ef3ab4065eec9e38dfa/libs/mcp/server/src/local_tools.rs#L1337),
[restore insertion](https://github.com/stakpak/agent/blob/d3eba6f502a79b993b826ef3ab4065eec9e38dfa/cli/src/commands/agent/run/mode_interactive.rs#L469),
[base prompt preservation](https://github.com/stakpak/agent/blob/d3eba6f502a79b993b826ef3ab4065eec9e38dfa/libs/api/src/local/hooks/task_board_context/mod.rs#L80).


Native qualification receipts: [curated JSON](assets/stakpak-20261011/qualification.json). Raw native logs, profiles, credentials and databases remain private on the VPS; they are not committed.
