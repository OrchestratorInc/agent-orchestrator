# Lessons from harness PRs #5507–#6553

These failures recurred across sixteen harness integrations: Cortex, ZCode,
Tau, Neovate, Open Interpreter, Memcode, Strands, MiniMax Code, Hermes,
OpenJarvis, Docker Agent, Mastra Code, CodeBuddy, Stakpak, Pool and Freebuff.
Each item says how to avoid it. Items marked **(automated)** are checked by
the runner, `harness_touchpoints.mjs` or `harness_pr_check.mjs`. The rest
belong in the adapter's design and tests.

## Before the first run

- **Do not book a run that cannot produce evidence.** The runner's host
  preflight **(automated)** fails fast on:
  - less than 1 GiB free
  - missing `git`/`sqlite3`/`go`
  - a TMPDIR long enough to break Unix sockets (`listen EINVAL`; the runner now
    uses `/tmp`)

  It also says up front when UI screenshots are impossible: no
  `frontend/node_modules`, under about 2 GiB free, or no capture command. Every
  PR lost its UI evidence to this. Provision dependencies and an uploader
  first, or record `AO UI evidence: BLOCKED` once, with that reason.
- **Verify auth before the run, not during it.** "Credentials configured, but
  AO could not verify them" ended strict runs in every PR.
  - Give the adapter a real verification probe. If none exists, declare the
    harness configured-only and send the user the login step immediately.
  - Never report `authorized` because a key exists in the environment.
  - Treat HTTP 429 and "Limit Exhausted" as quota (`needs_user`), not as an
    auth bug.
- **Scrub the launch environment.** An inherited `CI=1` hid a native composer
  **(automated: removed from the daemon env)**. Also run the native binary
  under a fake HOME and diff the real HOME to find writes that ignore its
  config-dir variable.
- **Give scratch projects a real `origin/HEAD`** **(automated)**. Without one,
  spawn fails with `DEFAULT_BRANCH_UNRESOLVED`.

## Contract and adapter design

- **Identity and version.**
  - Pick the version subcommand from `--help` (`version`, not always
    `--version`).
  - Match the version exactly; `strings.Contains` accepted lookalikes.
  - Check identity for generic names such as `interpreter`, because the first
    PATH match can be another program.
  - Pin the install recipe to the version the adapter admits, and compare the
    update advisory against that version.
  - Key receipts by binary hash.
  - In release-feed lookups, strip version specifiers for every manager (npm
    `@x`, pip/uv `==x`).
- **Ready and idle cues.**
  - Build cues from captured raw and styled frames, and match on ANSI-stripped
    text.
  - Anchor them to the bottom composer region, never anywhere in the last N
    lines. Transcripts and scrollback can print the cue (spoofing).
  - Require a current-launch signal, because stale tmux scrollback reported a
    restored session ready.
  - Add fixtures for spoofing, compact width, a non-English locale and a
    coloured draft.
- **Prompt delivery.**
  - An HTTP 200 or an empty composer is not proof of submission. Confirm the
    native saved user turn.
  - Use bracketed paste for multiline input, and test prompts over 8 KiB.
  - Handle native modal gates (take over, self-update, trust prompts, paste
    cards) explicitly as `needs_user` or contract steps.
- **Models.**
  - Make `selectionMode` match the real source. Use `catalog` only with a
    native list, profile or config read-through; otherwise use `text` and
    declare `modelSelection: "text"` in the contract **(automated check)**.
  - Match model IDs as whole tokens: `glm-5p3` must not match `glm-5p3-flash`.
  - On restore, drop saved models the catalog no longer offers.
- **Permissions and effort.**
  - Map every AO mode, including the default `auto`.
  - Restore must never keep a more permissive saved mode than the one
    requested (Neovate `yolo`).
  - Inherited effort clears silently, while an explicit unsupported effort is
    a typed refusal.
  - Add native effort support to the effort allowlist
    **(touchpoint: `effort-gate`)**.
- **Instructions.**
  - Seed a conflicting native `.env`/config and confirm AO's standing
    instructions still win. Hermes's native dotenv overrode them.
  - After Kill and restore with a changed private token, an ordinary task must
    use the new token. Several harnesses replayed the old system prompt from
    native history. Check that explicitly before blaming the model.
- **Restore negatives.** Each of these must give a fresh-launch fallback or a
  typed 4xx refusal, never a 500 and never a silent fresh conversation:
  - a missing native ID
  - missing or withheld history
  - a torn or oversized last record
  - a foreign workspace for the same ID
  - a whole-file hash invalidated by a harmless rewrite
- **Typed refusals.** Unsupported operations return a typed 4xx with the right
  sentinel: `TERMINAL_UNSUPPORTED` is not `CHAT_UNSUPPORTED`. Several PRs
  surfaced refusals as HTTP 500.
- **Shared code.** Changing the observer, ConPTY, ACP or the host for one
  harness changed Codex, Claude Code and others. Prefer harness-local code.
  When shared code must change, add Codex and Claude tests
  **(warned by `harness_pr_check.mjs`)**.
- **Native profile and storage.**
  - Copy every file the native CLI needs: permissions, region and plugins.
  - Exclude lock directories, and refresh copied credentials.
  - Fence native extension points (plugins, MCP servers, HOME overrides) that
    could bypass AO's validation.

## Touchpoints the checklist used to miss

Rerun `harness_touchpoints.mjs` after every merge from main **(automated)**.
New rows cover:

- **Terminal refresh:** the adapter implements `SupportsTerminalVersionRefresh`,
  and the harness is listed in both refresh test matrices. Missing this turned
  #6546 and #6541 red after a merge.
- **`ao doctor` probe spec:** needed when the binary name differs or a version
  is pinned.
- **Effort allowlist** (`effort-gate`).
- **Docs agents index** (`docs-agents-index`).

Reviewer registration, avatar and licence, the docs table and spawn help were
already rows, and were still skipped in most PRs. Treat each `TODO` as
unfinished wiring, not optional polish.

## PR hygiene

**(automated by `harness_pr_check.mjs`)**

- Keep audit reports, receipts, screenshots and `harness-proof-build.yml` out of
  the diff. They go in PR comments and attachments.
- Allocate migrations from the generator, above main's latest and any open
  sibling PR's. Collisions forced up to five renumbers on one PR.
- Read failed CI logs and fix them before posting anything. Two PRs looped on
  "waiting for CI" while lint was red.
- Keep the PR scoped: no unregistered drivers in a TUI PR, and no unrelated
  repairs.
- A merge does not close open audit gates. List the gates still open in the PR
  and in a follow-up (Memcode merged with auth and UI unqualified).
