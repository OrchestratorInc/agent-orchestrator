# Tau terminal harness

AO integrates the native Textual TUI from [Tau v0.4.7](https://github.com/huggingface/tau/releases/tag/v0.4.7), commit `e4eab0dc5d6c7a92dc40e660087e55f0d5929eba`. The install plan pins `tau-ai==0.4.7`; launch and native restore reject other versions until their contract has been reviewed.

Tau requires **explicit bypass permissions** in AO. Tau's `--approve` trusts project instructions for one process; it does not implement a tool approval policy. Default/manual, accept-edits, and auto modes are rejected. Tool allow/deny restrictions are also rejected on launch and restore because Tau cannot enforce them. Configure a provider through Tau's native `/login` flow first. AO reports configured credentials as `configured`, never as verified authorization.

The integration launches `tau --cwd <workspace> --approve --no-extensions --extension <AO observer> --new-session`. The task is delivered through the mounted composer after observing `Ask Tau…` and native startup readiness. Task text never enters CLI arguments: values such as `sessions` and `setup` are commands in Tau's CLI. Missing composer evidence fails delivery instead of injecting text into an unknown screen.

AO writes a workspace-local observer with an ownership sentinel, refuses to overwrite user files, and excludes its artifacts from git. `--no-extensions` disables discovery; the explicit AO extension loads without modifying user-global configuration. Native project `AGENTS.md` discovery remains intact. Hidden AO standing instructions use a separate file passed to `--append-system-prompt`; Tau does not persist this additive prompt, so AO reapplies it on restore.

The observer reports native `session_start`, `agent_start`, canonical user `message_end`, `agent_settled`, and quit-only `session_shutdown`. Accepted user messages provide semantic prompt acceptance. `agent_settled` follows persistence and retry reconciliation; earlier `agent_end` is not treated as idle. The process supervisor supplies exit detection when shutdown hooks do not run.

Restore requires the exact 32-character lowercase hexadecimal native session ID and passes `--session <id>`. It never chooses the latest session. Before restoring, AO reads `tau sessions` and requires that the exact ID belongs to the same directory as the AO workspace (including symlink aliases); missing, ambiguous, or mismatched metadata fails restore. Cancellation sends Escape without Enter, including cancellation during controller handoffs. Model discovery parses Tau's local provider TSV into exact `provider/model` IDs. No Chat, reviewer, or native handoff-history capability is advertised.

## Source evidence and validation limits

The contract was inspected in these release-pinned sources:

- [CLI arguments, provider listing, and extension discovery](https://github.com/huggingface/tau/blob/v0.4.7/src/tau_coding/cli.py)
- [Mounted composer readiness and keyboard controls](https://github.com/huggingface/tau/blob/v0.4.7/src/tau_coding/tui/app.py)
- [Session persistence and settled events](https://github.com/huggingface/tau/blob/v0.4.7/src/tau_coding/session.py)
- [Extension event API](https://github.com/huggingface/tau/blob/v0.4.7/src/tau_coding/extensions/api.py)

Local tests, builds, provider installation, probes, and execution were **NOT_RUN** at the user's request. Unit tests and a dedicated `Tau released TUI` workflow were authored for remote CI. The workflow downloads the official `tau_ai-0.4.7-py3-none-any.whl`, verifies SHA-256 `4899ca3cc1c965e27c175fefad8754a68b441d9af49838f6624cc0e1adb59ce9`, and installs it only on the GitHub runner.

The opt-in `TestReleasedTauTUIConformance` test uses `AO_TAU_BINARY`, an isolated home/profile/workspace, and a localhost OpenAI-compatible endpoint. It drives the real native TUI through a PTY and verifies preserved default/project instructions, hidden additive context, multiline command-like input, exact native identity/workspace and missing-ID rejection, SIGKILL/restore, active/accepted/settled hooks, and Escape cancellation followed by continued input. Discovered user/project extensions are tripwired to verify that only the explicit observer loads. This is a fixture-provider contract test; authenticated external-provider behavior and platforms other than the workflow's Linux runner remain **NOT_RUN**.
