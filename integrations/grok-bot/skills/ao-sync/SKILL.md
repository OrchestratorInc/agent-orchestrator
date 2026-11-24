---
name: ao-sync
description: >-
  Make the user's computers match: copy their global dev setup (coding-agent
  configs, skills and MCP servers, shell profile, git config, editor settings,
  global tools) from a main computer to the others. Use for "sync my setup",
  "sync everything", "sync <x> only", "change my sync picks", "copy my settings
  to my other Mac", "make my computers match".
---
Follow the ao-rules skill. Each apply is a write and needs the user's yes, except saved picks the user said to apply on "sync everything". Needs two or more connected computers (ListMachines).

## Scope: global only
- **Include**: coding-agent CLIs (Claude Code, Codex, Cursor, OpenCode, Gemini): version, global rules (`CLAUDE.md`, `AGENTS.md`), skills, commands, agents and MCP server definitions. Shell profile (aliases, `PATH`, functions). Global git config (not credentials). Editor settings, keybindings, snippets and extension list (VS Code, Cursor, Zed). Global npm, pnpm, bun, pipx and uv tools. Runtime versions (node, python, go).
- **Never copy**: secrets, tokens, API keys, `*auth*`, `*token*` and credential files, `~/.ssh`, gpg keys, keychains, `.env`, session or history databases, caches, `~/.ao` (Orchestrator keeps its own state per host), project repositories. A config file with an inline secret (an MCP server's env, for example) is copied with that value removed and marked "fill in by hand".
- **Sign-ins** are listed as "sign in by hand: <tool> (`<login command>`)".

## First run
1. ListMachines. Ask which computer is the main one and the direction: main to all, one pair, or a two-way merge of picked items.
2. Scan both sides read-only, in parallel: for each candidate, whether it exists, its version, a sha256, and a one-line difference. Skip secret files by name without opening them.
3. Show a checklist grouped as Coding agents / MCP / Shell / Git / Editors / Global tools / Runtimes / Sign in by hand. Mark each `missing` (on the target), `differs`, `same` or `not portable`. The user picks by group or item.
4. Save the picks to `~/.ao-grok/sync-prefs.json` on the main computer (`main`, `targets`, `items` with `id`, `group`, `src` and `kind`: `file`, `dir`, `json-merge`, `tool-list` or `version`), and a memory note with the main computer and that path. Then apply.

## Later runs
- "Sync everything": re-scan only the saved items, show what will change on each target, apply as allowed. Mention anything new that isn't in the picks; don't apply it.
- "Sync <x> only": the same for the matching items.
- "Change my sync picks": re-scan, show the checklist with current picks marked, save.

## Apply
1. Back up every file you will overwrite on the target to `~/.ao-grok/sync-backups/<time>/` (same relative path).
2. Rewrite absolute home paths inside configs for the target. Move files through the box with CopyToBox and CopyFromBox; never print a file that may hold a secret.
3. `json-merge`: merge keys and keep keys only the target has. `tool-list`: install what is missing. `version`: upgrade to match, through ao-dev-update and its safety check.
4. Verify: hashes match, `<tool> --version` runs, JSON parses, a fresh shell loads the profile without errors.
5. Report per target: changed, added, skipped and why, the backup path, and sign-ins still needed. If a check failed, offer to restore from the backup.
