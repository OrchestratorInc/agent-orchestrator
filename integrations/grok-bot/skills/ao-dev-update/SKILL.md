---
name: ao-dev-update
description: >-
  Update tools on the user's computers by name or by level (Orchestrator,
  coding agents, dev tools, everything), with a safety check so nothing a
  running session or project depends on breaks. Use for "update claude and
  codex", "update gh", "update my agents", "update everything", "what's out of
  date", "change my update levels". Not this bot's own skills ("update
  Orchestrator Bot" is ao-update).
---
Follow the ao-rules skill. Only items resolved this turn are approved, or a saved level the user said to apply. Never touch a project's own lockfile or dependencies.

## 1. What to update
- **By name**: "update gh", "update claude and codex on the Pro". A host name narrows it.
- **By level**: the levels in `~/.ao-grok/update-prefs.json` on the main computer. First use: create it with these, ask whether any long-running job must never be disturbed (save it under `protect`), and show the survey:
  `"update agents": ["claude-code","codex"]`, `"update dev": ["claude-code","codex","gh","node","npm-global"]`, `"update everything": ["claude-code","codex","gh","node","npm-global","brew"]`.
- **"What's out of date"**: survey only.
- **"Change my update levels"**: show, edit, save. Don't update in that turn.

## 2. Survey (read-only, every host in scope)
- Coding agents, through Orchestrator, which also knows which sessions use them: `aoapi GET "agents/<id>/update-advisory?refresh=true"` gives `currentVersion`, `latestVersion`, `status` (`behind_latest` means an update exists) and `inUse.level`.
- Orchestrator itself: the app updates itself; `ao version` shows the version. Point the user to the app's update prompt.
- Others: `gh --version`, `node --version`, `npm -g outdated`, and on macOS `brew outdated`.
Show only what is out of date: host, item, current and latest version, and whether it is a major jump.

## 3. Safety check
- **Coding agents**: `inUse.level` decides. `safe`: go. `warn`: running sessions may notice; ask. `idle`: Orchestrator waits until those sessions are idle and restarts them around the update; say so. `blocked`: the sessions in `inUse.sessionIds` must end first; never end them for an update.
- **Runtimes and package managers**: in projects with sessions or commits in the last 14 days, read `.nvmrc`, `.node-version`, `.tool-versions`, `package.json` `engines` and `packageManager`, `go.mod`, `.python-version`. A major jump, or a new version outside a pin: ask, naming the project and pin.
- Anything in `protect` that is running (`pgrep -fl`): skip what it uses.
Show the plan as go, ask and blocked, and wait for answers on ask.

## 4. Apply
Note each current version first; it is the rollback.
- Coding agents: `aoapi POST "agents/<id>/update-advisory"`, then check `aoapi GET "agents/<id>/install"` every 15 seconds until `status` is `succeeded` or fails. Orchestrator updates the agent the way it was installed.
- `gh`, `node`: `brew upgrade <approved formulae>` on macOS, naming only the items that passed the safety check (`brew upgrade gh` for "update gh"; `node` or the installed `node@<major>` only when it passed too); Linux needs the package manager and usually `sudo`: give the command and let the user run it. Global npm tools: `npm i -g <pkg>@latest`. `brew` level: `brew upgrade <formulae>` with the approved items from `brew outdated`, never a bare `brew upgrade`. Homebrew also upgrades outdated dependencies and dependents of what it names, so run `brew upgrade --dry-run <formulae>` first: if it lists an item that didn't pass the safety check (for example `node` behind `openssl@3`), leave out the formula that pulls it in, or ask about that item first. Homebrew deletes the old version after an upgrade and can't switch an unversioned formula back (`brew switch` is gone): in the plan, mark the brew items that have no rollback.

## 5. Check and report
`<tool> --version` for each updated item; for agents, `aoapi GET "agents/<id>/update-advisory"` shows `current`. Per host: before and after, what was skipped and why. Anything broken: the error in one line and the rollback (`npm i -g <pkg>@<old>`, or `brew install <formula>@<old major>` for a versioned formula such as `node@20`), done only on a yes. An unversioned brew formula has none: say so.
