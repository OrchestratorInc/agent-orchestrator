# Integrating a harness into AO

The build guide for a new harness. Steps 1 and 2 of [SKILL.md](../SKILL.md).
Each "Avoid" note comes from earlier harness PRs, where the same mistakes made
16 integrations fail review or the audit.

## 1. Compatibility

Pin a released version, then fill this table from the CLI's real behaviour
(`--help`, a quick manual run), not from its README:

| Area | What to find out |
| --- | --- |
| Binary | Executable name, version command, supported OSes |
| Install | Official installers per OS (npm, brew, curl script, winget…) |
| Login | How it logs in (CLI login, API key, config), and a command that **verifies** the login |
| TUI | An interactive mode AO can launch, and how a first prompt reaches it |
| Chat | A structured protocol: ACP (Agent Client Protocol) or an SDK/app server |
| System prompt | How to pass hidden instructions: a flag or file argument, a Chat session parameter, or a config file |
| Activity | Hooks or events for working / idle / waiting, and the CLI's own session ID |
| Restore | Resume a session **by ID** (never "the latest one") |
| Models | A model list command, a config file, or free-form only |
| Permissions | What maps to AO's manual, accept-edits, auto and bypass modes |

Write down what it does not support, with proof. That becomes a clear "not
supported" state in AO, not a gap.

## 2. Build

Copy the closest existing harness. `harness_touchpoints.mjs --like <id>` prints
where that harness is wired in every file. Rerun it after each merge from main.

1. **TUI adapter** (`backend/internal/adapters/agent/<id>/`): launch command,
   first-prompt delivery, hooks/activity, session-ID capture, resume by ID,
   system-prompt channel, permission modes, terminal version refresh.
2. **Chat driver** (`backend/internal/adapters/chatdriver/<id>acp/` or an SDK
   driver), only when the protocol works for session start, load or resume,
   permissions and cancel.
3. **Installer** for every OS and installer the CLI officially supports, so the
   Install button works (`install-target`, `install-plan`).
4. **Models:** a real catalog (`selectionMode: catalog`) from the CLI's list or
   config; otherwise free-form (`text`). Add native effort levels to the effort
   allowlist.
5. **Auth detection** on macOS, Linux and Windows, and a **Log in / Set up**
   action (`auth-plan`).
6. **Connected state:** once logged in, the Harness page shows Connected.
7. Branding (logo, licence), docs, `ao doctor` probe, reviewer if supported.

### Avoid (from earlier PRs)

**Auth and install**
- Reporting `authorized` because a key exists. Credentials that are present
  but unchecked are `configured`; give the adapter a real verification probe.
  "Credentials configured, but AO could not verify them" ended most earlier
  audits.
- Treating HTTP 429 / "limit exhausted" as an auth bug: it is quota, and
  needs the user.
- Loose version matching (`strings.Contains`) or trusting the first PATH match
  for a generic binary name: check identity and the exact version.

**Prompt delivery and ready cues**
- Believing an HTTP 200 or an empty input box means the prompt was submitted:
  confirm the CLI saved the user turn. Use bracketed paste for multi-line
  prompts and test one over 8 KiB.
- Ready cues that match anywhere in the last lines: anchor them to the bottom
  input box, so scrollback that prints the cue cannot fake "ready". Build them
  from real captured frames, ANSI-stripped.
- Ignoring native prompts (trust this folder, self-update, take over a
  session): handle each one, or report it as needing the user.

**System prompt.** Choose the channel before writing the adapter, best first:
1. a native flag or file argument (e.g. `--append-system-prompt-file`);
2. a Chat session parameter (e.g. ACP `_meta.systemPrompt`);
3. a config or env file the CLI loads on top of its defaults;
4. last resort: a session-scoped rule file the CLI auto-applies, kept in the
   AO data dir or git-ignored, and proven to load (a CLI can ignore it);
5. none: fail clearly. Never paste private instructions into the visible prompt.

Writing `system.md` is not delivery; something must pass it to the CLI. A
native `.env` or config can override AO's instructions, so test with one present.

**Restore**
- A missing session ID, missing history or a foreign workspace must give a
  fresh-launch fallback or a typed 4xx, never a 500 and never a silent new
  conversation.
- Restore must not keep a more permissive saved permission mode than the one
  requested.
- Drop saved models the catalog no longer offers.

**Models.** Match IDs as whole tokens (`glm-5` must not match `glm-5-flash`).
Use `catalog` only with a real source.

**Shared code.** A change to the shared observer, ACP, ConPTY or host code for
one harness changes every harness. Keep code harness-local; when shared code
must change, add tests for Codex and Claude Code too.

**Environment.** An inherited `CI=1` hides some TUIs' input box (the audit
removes it). Run the CLI under a fake HOME once and check it writes only where
its config-dir variable says.

## 3. Register and publish

- Register a surface only after its audit checks pass. Unsupported operations
  return a typed 4xx with the right code (`TERMINAL_UNSUPPORTED` is not
  `CHAT_UNSUPPORTED`).
- Migrations: take the next number from the generator, above main's latest and
  any open sibling PR's.
- Keep the PR to the harness. No unrelated fixes, no unregistered drivers.
- Keep audit reports, screenshots and evidence **out of the diff**. Post them as
  PR comments.
- Read failed CI logs and fix them before asking for review.
- List any check still open in the PR description. A merge does not close it.
