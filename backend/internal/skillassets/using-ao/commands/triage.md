# ao triage

Unreleased. Investigate a problem with Claude Code or Codex, even with the daemon down.
It collects bounded, redacted log tails, app and agent versions, daemon state,
recent session metadata, GitHub availability, and possible duplicate issues.

```bash
ao triage
ao triage --agent codex
ao triage --no-launch --json
```

Files go under `$AO_DATA_DIR/triage` when set, otherwise `~/.ao/triage`.
Each run writes `context.md`, `bug-report.md`, `PLAYBOOK.md`, and `prompt.md`.
The agent asks what went wrong and fills the draft. After leaving the terminal
agent, review the proposed text and answer the posting prompt `[y/N]`.
Declining keeps the draft. `--no-launch` only prepares files; `--model` selects
an agent model. Collection never starts or repairs the daemon.
