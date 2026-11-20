---
name: ao-cleanup
description: >-
  Free disk space: a read-only audit of what uses the disk on a host, first
  Orchestrator's own space (worktrees of ended sessions), then the rest of the
  computer, ending in Safe, Ask and Never lists. Deletes only what the user
  picks. Use for "clean up my Mac", "free up space", "disk is full", "what is
  using my disk", "clean up old sessions".
---
Follow the ao-rules skill. The audit is read-only. Every delete needs the user's yes for those exact items.

## 1. Orchestrator's own space
Ended sessions keep their worktrees until cleaned. Preview, read-only:
```sh
ao session cleanup --dry-run
```
Add `--project <id>` for one project. It lists the sessions whose workspaces it would remove; worktrees with uncommitted changes are skipped by Orchestrator itself. On yes: `ao session cleanup --yes` (same filters). Remote host: `aoapi POST sessions/cleanup` cleans every ended session, `sessions/cleanup?project=<id>` one project; it has no preview, so ask with the count from ao-status first.

Never delete anything under `~/.ao` by hand. Its database, worktrees and settings belong to Orchestrator.

## 2. The rest of the computer (local hosts)
Measure, read-only, the biggest folders under the home folder and the usual caches:
```sh
f(){ df -h ~ | tail -1; for d in ~/Library/Caches ~/.cache ~/Library/Developer/Xcode/DerivedData ~/Library/Developer/CoreSimulator ~/.npm ~/.pnpm-store ~/Library/pnpm ~/.cargo/registry ~/go/pkg/mod ~/.gradle/caches ~/Library/Containers/com.docker.docker ~/.docker ~/Downloads ~/.Trash; do [ -e "$d" ] && du -sh "$d" 2>/dev/null; done
  du -xsh ~/* ~/.[!.]* 2>/dev/null | sort -rh | head -15; }; f
```
On Linux, `~/.cache`, `~/.local/share/Trash` and the package manager's cache stand in for the macOS folders. `du` on a big home folder can take a minute or two: say so first.

## 3. Lists
Show three lists with sizes:
- **Safe**: rebuildable caches (`~/Library/Caches`, `~/.cache`, Xcode DerivedData, package manager caches via their own command: `npm cache clean --force`, `pnpm store prune`, `go clean -modcache`), the Trash, `docker system prune` (it removes stopped containers and unused images: say so).
- **Ask**: Downloads, old simulators (`xcrun simctl delete unavailable`), large files the user may still want, `node_modules` in projects not touched for 90 days.
- **Never**: `~/.ao`, project repositories and worktrees, keychains, `~/.ssh`, credentials, mail, photos, cloud-synced folders, anything under `/System` or `/Library`.
Let the user pick by list or by item. Delete only the picks, with the tool's own clean command where one exists, else `rm -rf -- '<exact path>'`. Never `sudo`. Then `df -h ~` again and report what was freed.
