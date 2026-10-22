# Side-chat reliability: issue #6564

Base: upstream `464fa1ee943ddc2b5fd356c6b6ccb1ffe6ab9af8`.
Branch: `codex/fix-6564-side-chat-reliability`.

Severity measures user impact; complexity measures implementation and verification
effort. Both use a scale of 1–10. No P0 is established.

| ID | Issue | Priority | Severity | Complexity | Status |
| --- | --- | --- | --- | --- | --- |
| SIDE-01 | Stale running-turn recovery blocks its queue | Reported P1 | 8 if reproduced | 4 | Existing fix verified through public recovery API |
| SIDE-02 | Side-local commands duplicate delivery | P2 | 6 | 7 | Pending phase 2 |
| SIDE-03 | Side-local commands omit staged files | P2 | 5 | 5 | Pending phase 2 |
| SIDE-04 | Close loses cleanup ownership during a crash | P2 | 6 | 7 | Pending phase 3 |

## Phase 1

The integration regression serializes an older running snapshot, restores it via
`RecoverSideChatLaunch`, and supplies fresh terminal history from the resumed
provider. The completed turn is settled and only the queued question dispatches.
An unknown outcome visibly fails recovery, preserves queued work, and sends no
prompt. Both cases pass on Windows; no production recovery change was needed.

## Validation limits

Full-suite, race, and isolated desktop results will be recorded as phases finish.
Developer Mode gating and launch-scoped recovery remain unchanged. Performance,
visual redesign, and long-history reconstruction are outside this branch.
