---
name: ao-setup
description: >-
  First run of Orchestrator Bot: find every computer that runs AO, save them as AO
  hosts, add a remote AO host that has no Grok Bot desktop app, and turn on
  optional routines. Use on the first chat, when memory has no "AO hosts" note,
  or when the user says "set up AO", "add a host", "add remote host" or
  "check my AO setup".
---
Follow the ao-rules skill. Detection is read-only. Nothing is written until the user says yes.

Open with one line: "Give me a few seconds to look for AO on your computers."

## 1. Detect
1. ListMachines. Use desktops only. If none is connected, say: "Install the Grok Bot desktop app on the computer that runs AO, sign in, and tell me when it shows up," then stop.
2. On each connected desktop, in parallel, run this read-only probe with the `aoapi` helper from ao-rules: run "Probe" below.
3. Each desktop where this prints projects is a **local** AO host. On a Windows desktop, don't run the probe: say once "These skills need macOS or Linux; Windows isn't supported yet," and skip it.

### Probe
```sh
f(){ <aoapi helper>; echo "ao=$(command -v ao >/dev/null && ao version 2>/dev/null | head -1 || echo missing)"
  echo "python=$(command -v python3 >/dev/null && python3 -c 'import sys;print("%d.%d"%sys.version_info[:2])' || echo missing)"
  aoapi GET projects | python3 -c 'import json,sys; d=json.load(sys.stdin); p=d.get("projects",d) if isinstance(d,dict) else d; print("projects=" + ", ".join(x.get("id","?") for x in p))'
  aoapi GET 'sessions?active=true' | python3 -c 'import json,sys; print("live_sessions=%d" % len(json.load(sys.stdin)["sessions"]))'; }; f
```

## 2. Report and ask once
One message, at most 6 lines: per computer, AO found or not, its projects, and how many sessions are live. Then:
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
