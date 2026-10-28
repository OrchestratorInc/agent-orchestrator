---
name: ao-setup
description: >-
  First run of Orchestrator Bot: find every computer that runs AO, save them as
  AO hosts, add a remote AO host that has no Grok Bot desktop app, and turn on
  optional routines. On a computer without Orchestrator.inc it helps install it
  and get it ready: a coding agent, a first project and the first orchestrator.
  Use on the first chat, when memory has no "AO hosts" note, or when the user
  says "set up AO", "add a host", "add remote host", "check my AO setup" or
  "install Orchestrator".
---
Follow the ao-rules skill. Detection is read-only. Nothing is written until the user says yes.

Open with one line: "Give me a few seconds to look for AO on your computers."

## 1. Detect
1. ListMachines. Use desktops only. If none is connected, say: "Install the Grok Bot desktop app on the computer where you run Orchestrator.inc (or want to), sign in, and tell me when it shows up," then stop.
2. On each connected desktop, in parallel, run this read-only probe with the `aoapi` helper from ao-rules: run "Probe" below.
3. Each desktop where the probe prints a `projects=` line is a **local** AO host; an empty list means it still needs "First run". For any other result, see "Missing pieces". On a Windows desktop, don't run the probe: say once "These skills need macOS or Linux; Windows isn't supported yet," and skip it.

### Probe
```sh
f(){ <aoapi helper>; echo "ao=$(command -v ao >/dev/null && ao version 2>/dev/null | head -1 || echo missing)"
  if [ "$(uname -s)" = Darwin ] && [ "$(command -v python3)" = /usr/bin/python3 ] && ! xcode-select -p >/dev/null 2>&1; then echo "python=missing"
  else echo "python=$(command -v python3 >/dev/null && python3 -c 'import sys;print("%d.%d"%sys.version_info[:2])' || echo missing)"; fi
  [ "$(uname -s)" = Darwin ] && A=$(find /Applications "$HOME/Applications" -maxdepth 1 -name '*Orchestrator*.app' 2>/dev/null | head -1) && echo "app=${A:-missing}"
  if P=$(aoapi GET projects 2>/dev/null); then
    echo "$P" | python3 -c 'import json,sys; d=json.load(sys.stdin); p=d.get("projects",d) if isinstance(d,dict) else d; print("projects=" + ", ".join(x.get("id","?") for x in p))'
    aoapi GET 'sessions?active=true' | python3 -c 'import json,sys; print("live_sessions=%d" % len(json.load(sys.stdin)["sessions"]))'
  else echo "daemon=not running"; fi; }; f
```

## Missing pieces
Use the probe result for each computer. Install or open anything only after the user's yes.

| Probe shows | Next step |
|---|---|
| `python=missing` | Fix this first: every step needs it. macOS: `xcode-select --install` (the user confirms the system dialog). Linux: give the package manager line for python3 |
| `daemon=not running` and `app=missing` (macOS) | Say: "Orchestrator.inc isn't installed on <computer>. Want me to install it? It's about a 300 MB download." On yes, run "Install on a Mac" |
| `daemon=not running` and `app=<path>` | On yes, open it with `open "<path>"`, then "Wait for the app" |
| `daemon=not running` on Linux | If Orchestrator.inc is installed, ask the user to open it and say **check again**. Otherwise give the download for the user's distribution: [AppImage](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.AppImage), [.deb](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.deb) or [.rpm](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.rpm). The user installs and opens it, then says **check again** |
| `projects=` with nothing after it | Run "First run" |
| `ao=missing` with projects listed | Everything works except starting a review, screenshots and automations, which use the `ao` CLI. Say so once |

**check again** runs the probe on that computer again and goes on from its result.

**Wait for the app:** run the probe every 15 seconds, for up to 2 minutes, until it prints a `projects=` line. Still `daemon=not running` after that: ask the user to look at the Orchestrator.inc window and say **check again** when it's open.

### Install on a Mac
After a yes, on that computer. The download takes a few minutes, so the install runs in the background, in its own session so it keeps going after this command returns, and writes its progress to `~/.ao-grok/install.log`. It picks the build for the Mac's chip, checks the app's code signature (Orchestrator.inc's Developer ID team `X9LNSP8MC2`), copies it to Applications (or `~/Applications` if that isn't writable) and opens it:
```sh
f(){ mkdir -p ~/.ao-grok && cat > ~/.ao-grok/install-orchestrator.sh <<'EOF'
a=$([ "$(sysctl -n hw.optional.arm64 2>/dev/null)" = 1 ] && echo arm64 || echo x64); d=$(mktemp -d)
echo "downloading the $a build"
curl -fsSL -o "$d/o.dmg" "https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-$a.dmg" || { echo DOWNLOAD_FAILED; rm -rf "$d"; exit 1; }
m=$(hdiutil attach -nobrowse -readonly "$d/o.dmg" | awk -F'\t' '/\/Volumes\//{print $NF}' | tail -1)
[ -n "$m" ] || { echo INSTALL_FAILED; rm -rf "$d"; exit 1; }
app=$(find "$m" -maxdepth 1 -name '*.app' 2>/dev/null | head -1)
done_(){ [ -n "$m" ] && hdiutil detach -quiet "$m"; rm -rf "$d"; }
if [ -z "$app" ] || ! codesign --verify --deep --strict "$app" 2>/dev/null || ! codesign -dv "$app" 2>&1 | grep -q 'TeamIdentifier=X9LNSP8MC2'; then echo BAD_SIGNATURE; done_; exit 1; fi
dest=/Applications
cp -R "$app" "$dest/" 2>/dev/null || { dest="$HOME/Applications"; mkdir -p "$dest" && cp -R "$app" "$dest/"; } || { echo INSTALL_FAILED; done_; exit 1; }
done_
open "$dest/${app##*/}" && echo "INSTALLED $dest/${app##*/}" || echo "INSTALLED_NOT_OPENED $dest/${app##*/}"
EOF
nohup python3 -c 'import os,sys; os.setsid(); os.execvp("sh", ["sh", sys.argv[1]])' ~/.ao-grok/install-orchestrator.sh > ~/.ao-grok/install.log 2>&1 & echo STARTED; }; f
```
Tell the user it has started. Then check `tail -1 ~/.ao-grok/install.log` every 20 seconds, for up to 15 minutes:

| Last line | Next step |
|---|---|
| `downloading …` | Still going |
| `INSTALLED <path>` | Say "Orchestrator.inc is open on <computer>. I'll get it ready: a coding agent and your first project." Then "Wait for the app" and run "First run" |
| `INSTALLED_NOT_OPENED <path>` | Open it with `open "<path>"`, then as above |
| `DOWNLOAD_FAILED`, `BAD_SIGNATURE`, `INSTALL_FAILED`, or no change after 15 minutes | Say what happened and give the user the [download page](https://orchestrator.inc/download/) instead. Never retry `BAD_SIGNATURE` |

## First run
Gets a computer ready for work once Orchestrator.inc is running there: a coding agent, a project, and the first orchestrator. Every change needs the user's yes. If Orchestrator.inc shows its own welcome steps and the user prefers them, let them finish there and say **check again**.

### 1. A coding agent
```sh
f(){ <aoapi helper>; aoapi POST agents/refresh '{}' >/dev/null; aoapi GET agents/readiness | python3 -c '
import json,sys
for a in json.load(sys.stdin).get("agents") or []:
    inst=(a.get("installation") or {}).get("state",""); auth=(a.get("authentication") or {}).get("state","")
    if inst!="not_installed" or a.get("id") in ("claude-code","codex"):
        print("\t".join([a.get("id",""), a.get("label",""), a.get("effectiveReadiness",""), inst, auth]))'; }; f
```
Columns: id, name, readiness, installed, signed in.
- **Some agent is `ready`:** use it. If more than one is ready, ask which one should do the work. Don't pick for the user.
- **Installed but not signed in:** on yes, `aoapi POST "agents/<id>/auth" '{}'` and relay its `guidance`. The sign-in runs in a terminal inside Orchestrator.inc; the user finishes it there (it may open their browser). When they say done, run this step again: it refreshes the checks.
- **None installed:** ask "Which coding agent do you want to use: Claude Code or Codex?" On the pick and a yes, `aoapi POST "agents/<id>/install" '{}'`, then check `aoapi GET "agents/<id>/install"` every 15 seconds. `status` `succeeded`: sign in as above. `failed`, `unsupported` or `interrupted`: relay it, and point the user to the agent settings in Orchestrator.inc.

### 2. A project
Suggest the git clones the user worked in most recently (read-only; worktrees are left out):
```sh
python3 -c '
import glob,os
home=os.path.expanduser("~"); found=set()
for r in ("Development","Projects","projects","code","Code","src","dev","GitHub","Documents/GitHub"):
    for pat in ("*/.git","*/*/.git","*/*/*/.git"): found.update(os.path.dirname(g) for g in glob.glob(os.path.join(home,r,pat)) if os.path.isdir(g))
for p in sorted(found,key=lambda p:-os.path.getmtime(os.path.join(p,".git")))[:10]: print(p.replace(home,"~",1))'
```
Show them as one card, plus a typed option for another folder. On the pick, register it with the agent from step 1:
```sh
f(){ <aoapi helper>; aoapi POST projects "$(python3 -c 'import json,os,sys; print(json.dumps({"path":os.path.expanduser(sys.argv[1]),"config":{"worker":{"agent":sys.argv[2]},"orchestrator":{"agent":sys.argv[2]}}}))' '<repo path>' '<agent id>')" 2>/dev/null | python3 -c '
import json,sys
d=json.load(sys.stdin); p=d.get("project",d)
if p.get("id"): print("added", p["id"])
else: print(d.get("code","ERROR"), d.get("message",""), (d.get("details") or {}).get("existingProjectId",""))'; }; f
```
- `added <id>`: done.
- `NOT_A_GIT_REPO`: the folder needs to be a git repository with at least one commit. Ask for another folder.
- `PATH_ALREADY_REGISTERED <id>`: it is already a project; use that id.

### 3. The first orchestrator
Ask: "Want me to start the orchestrator for <project>? Tell me what you'd like done first, or I'll ask it to look around the repo and suggest a first task." On yes, run ao-orchestrator "Start an orchestrator" with that request.

Then continue with section 2.

## 2. Report and ask once
One message, at most 6 lines: per computer, AO found or not, its projects, and how many sessions are live. For a computer without a working AO, add the one next step from "Missing pieces". Then:
"Reply **yes** and I'll save these as your AO hosts. Is AO also running on a computer without the Grok Bot desktop app? Say **add remote host** and I'll connect to it from one of these."

On yes, save the **AO hosts** note (ao-rules "AO hosts") with `reach: local` and `via` = the same computer. Name each host with its short name (ao-rules `name`) and say the names in one line: "I'll call them Pro and Air. Say if you want other names."

## 3. Remote host (optional)
Use this when AO runs on a computer that has no Grok Bot desktop app, for example a Mac mini host that a laptop's AO app already connects to.

1. Ask for a short host name (letters, digits, `-`) and which connected computer to use as `via`. Default: the one the user's message came from.
2. Tell the user, in one message:
   "On the host, open AO, go to remote access (Connect Mobile) and turn on network access. It shows an address and a password. Send me the **address** here. You'll type the password on <via> in a window I open, so it never passes through this chat."
   On the host's own terminal, `ao remote-host enable` turns on the same network access, and `ao remote-host status` shows the address and the password.
3. When the address arrives, check it looks like `http://<ip or name>:<port>`. Then, on `via`, write a one-time password window and open it. Replace `<name>` with the host name you checked: run "Password window" below.
   Tell the user: "A Terminal window opened on <via>. Type the password there and press Return."
   On Linux, `open` doesn't run `.command` files: write the same lines to `~/.ao-grok/add-<name>.sh` and ask the user to run `zsh ~/.ao-grok/add-<name>.sh` in a terminal on `via`.
4. When they say done, test it from `via`: run "Connection test" below.
   HTTP 401 means a wrong password: open the window again. A timeout means the host is not reachable from `via` (another network, asleep, or access off).
5. Add the host to the **AO hosts** note with `reach: remote`, `via`, `base` and `header`.

### Password window
```sh
f(){ mkdir -p ~/.ao-grok/out && cat > ~/.ao-grok/add-<name>.command <<'EOF'
#!/bin/zsh
umask 077
read -rs "p?AO password for <name>: "; echo
printf 'Authorization: Bearer %s\n' "$p" > ~/.ao-grok/<name>.header
echo "Saved. You can close this window."; rm -- "$0"
EOF
chmod +x ~/.ao-grok/add-<name>.command && open ~/.ao-grok/add-<name>.command; }; f
```

### Connection test
```sh
f(){ <aoapi helper>; AO_REMOTE_BASE='<address>' AO_REMOTE_HEADER="$HOME/.ao-grok/<name>.header" aoapi GET 'sessions?active=true' | python3 -c 'import json,sys; print("live_sessions=%d" % len(json.load(sys.stdin)["sessions"]))'; }; f
```

Limits of a remote host, said once: keep-awake, browser screenshots and starting a review work only on local hosts. For those, ask the host's orchestrator (ao-orchestrator). Quick-tunnel addresses change when the tunnel restarts, so prefer the LAN address or a named tunnel. If the user presses Regenerate in AO, run this section again.

## 4. Mode and routines
1. Save **Mode: default** in memory (ao-rules "Decisions"). Say in one line that away mode exists: "Say 'I'm away till 6' and I'll keep routine work moving."
2. Offer the routines in one multi-select card, nothing pre-selected, plus **none**:
   - **AO waiting check**: every 30 minutes, 9:00 to 19:00 on weekdays. Runs ao-answer "Find what's waiting" and messages only when something new is waiting.
   - **AO daily digest**: weekdays at 9:00. Runs ao-status "Digest".
   Create the picked routines, then save "Routines on: <names>" in memory. Never create a second copy.
3. Say once: "AO's mobile app pushes questions to your phone the moment they happen. These routines check on a schedule."

## 5. Finish
One line: the hosts saved (local and remote), the mode and the routines on. Then run ao-status as a first look, then:

Things to try:
- what's waiting on me
- catch me up on <project>
- in <project>: <what you want done>
- I'm away till 6, keep things moving
- what's ready to merge
- show me what <session> built
