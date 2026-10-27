---
name: ao-setup
description: >-
  First run of Orchestrator Bot: find every computer that runs AO, save them as AO
  hosts, add a remote AO host that has no Grok Bot desktop app, and turn on
  optional routines. Use on the first chat, when memory has no "AO hosts" note,
  or when the user says "set up AO", "add a host", "add remote host",
  "check my AO setup" or "install Orchestrator". Helps install and open
  Orchestrator.inc on a computer that doesn't have it yet.
---
Follow the ao-rules skill. Detection is read-only. Nothing is written until the user says yes.

Open with one line: "Give me a few seconds to look for AO on your computers."

## 1. Detect
1. ListMachines. Use desktops only. If none is connected, say: "Install the Grok Bot desktop app on the computer that runs AO, sign in, and tell me when it shows up," then stop.
2. On each connected desktop, in parallel, run this read-only probe with the `aoapi` helper from ao-rules: run "Probe" below.
3. Each desktop where this prints projects is a **local** AO host. For any other result, see "Missing pieces". On a Windows desktop, don't run the probe: say once "These skills need macOS or Linux; Windows isn't supported yet," and skip it.

### Probe
```sh
f(){ <aoapi helper>; echo "ao=$(command -v ao >/dev/null && ao version 2>/dev/null | head -1 || echo missing)"
  echo "python=$(command -v python3 >/dev/null && python3 -c 'import sys;print("%d.%d"%sys.version_info[:2])' || echo missing)"
  [ "$(uname -s)" = Darwin ] && echo "app=$([ -d '/Applications/Agent Orchestrator.app' ] && echo installed || echo missing)"
  if P=$(aoapi GET projects 2>/dev/null); then
    echo "$P" | python3 -c 'import json,sys; d=json.load(sys.stdin); p=d.get("projects",d) if isinstance(d,dict) else d; print("projects=" + ", ".join(x.get("id","?") for x in p))'
    aoapi GET 'sessions?active=true' | python3 -c 'import json,sys; print("live_sessions=%d" % len(json.load(sys.stdin)["sessions"]))'
  else echo "daemon=not running"; fi; }; f
```

## Missing pieces
Use the probe result for each computer. Install or open anything only after the user's yes.

| Probe shows | Next step |
|---|---|
| `app=missing` (macOS) | Say: "Orchestrator.inc isn't installed on <computer>. Want me to install it?" On yes, run "Install on a Mac" |
| `app=installed` and `daemon=not running` | Open it with `open -a "Agent Orchestrator"`, wait 15 seconds, and run the probe again |
| `daemon=not running` on Linux | Give the download for the user's distribution: [AppImage](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.AppImage), [.deb](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.deb) or [.rpm](https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-linux-x64.rpm). The user installs and opens it, then says **check again** |
| `projects=` with nothing after it | Say: "Orchestrator.inc is running on <computer> but has no projects yet. Open it, add the repository you want it to manage, then say **check again**." |
| `python=missing` | macOS: `xcode-select --install` (the user confirms the system dialog). Linux: give the package manager line for python3 |
| `ao=missing` but projects listed | Everything works except starting a review, screenshots and automations, which use the `ao` CLI. Say so once |

"check again" runs the probe on that computer again and continues from section 2.

### Install on a Mac
After a yes, on that computer. It downloads the latest signed release for the Mac's chip, copies the app to Applications and opens it:
```sh
f(){ a=$([ "$(uname -m)" = arm64 ] && echo arm64 || echo x64); d=$(mktemp -d)
  curl -fsSL -o "$d/o.dmg" "https://github.com/OrchestratorInc/agent-orchestrator/releases/latest/download/agent-orchestrator-darwin-$a.dmg" || { echo DOWNLOAD_FAILED; rm -rf "$d"; return 1; }
  m=$(hdiutil attach -nobrowse -readonly "$d/o.dmg" | awk -F'\t' '/\/Volumes\//{print $NF}' | tail -1)
  cp -R "$m/Agent Orchestrator.app" /Applications/; r=$?
  hdiutil detach -quiet "$m"; rm -rf "$d"
  [ $r -eq 0 ] && open -a "Agent Orchestrator" && echo INSTALLED || echo INSTALL_FAILED; }; f
```
The download is about 300 MB. Tell the user before you start. On `INSTALLED`: "Orchestrator.inc is open on <computer>. Follow its first-run steps (sign in to your coding agent and add the repository you want it to manage), then say **check again**." On a failure, give the user the [download page](https://orchestrator.inc/download/) instead.

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
