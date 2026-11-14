---
name: ao-update
description: >-
  Update this bot's ao-* skills to the latest published version: check what
  changed, show it, and after the user's yes download each changed skill,
  verify its sha256 against the published manifest, and rewrite it, keeping
  skills the user edited. Also the update mode (auto, notify, off) and the
  weekly update routine. Use for "update Orchestrator Bot", "update the AO
  bot", "update my AO skills", "what's new in Orchestrator Bot", "update
  automatically", "just tell me about updates", "stop checking for updates",
  "reset <skill>", "restore my <skill>", and the Orchestrator Bot updates
  routine.
---
Follow the ao-rules skill. This runs on the bot's own cloud computer (the box), not on the user's computers. It changes only this bot's ao-* skills, and only after the user's yes or in auto mode.

The skills are published in the public Orchestrator repository (`OrchestratorInc/agent-orchestrator`, folder `integrations/grok-bot/`) with a `manifest.json` that lists each skill's sha256. Every check reads the newest commit on `main`, then downloads the manifest and the skills from that exact commit, so the files always match each other.

## 1. Save the fetcher
Save it on the box if `/workspace/ao-bot/fetch.py` is missing or differs:
```sh
f(){ mkdir -p /workspace/ao-bot && cat > /workspace/ao-bot/fetch.py <<'PY'
import datetime, hashlib, json, os, re, shutil, subprocess, sys, tempfile
REPO, DIR = "OrchestratorInc/agent-orchestrator", "integrations/grok-bot"
SRC = os.environ.get("AO_BOT_SOURCE")  # a local folder instead of GitHub, for testing
OLD_SRC = os.environ.get("AO_BOT_OLD_SOURCE")  # the installed version's folder, for testing
STATE = os.environ.get("AO_BOT_STATE", "/workspace/ao-bot")
LIVE = os.environ.get("AO_BOT_LIVE", "/home/box/agent-data/workflows")
PENDING = os.path.join(STATE, "pending")
def get(url):  # curl, so the system's certificates are used
    r = subprocess.run(["curl", "-fsSL", "--max-time", "30", "-H", "User-Agent: ao-bot", url], capture_output=True)
    if r.returncode: sys.exit(f"DOWNLOAD_FAILED {url} {r.stderr.decode().strip()}")
    return r.stdout
def read(sha, path, src=None):
    if src: return open(os.path.join(src, path), "rb").read()
    return get(f"https://raw.githubusercontent.com/{REPO}/{sha}/{DIR}/{path}")
def jload(p, default):
    return json.load(open(p)) if os.path.exists(p) else default
def jsave(p, v):
    os.makedirs(os.path.dirname(p), exist_ok=True); json.dump(v, open(p, "w"), indent=1)
def parse(text):  # name, description and body of a SKILL.md, whatever YAML style it was saved in
    m = re.match(r"﻿?---\n(.*?)\n---\n?(.*)\Z", text.replace("\r\n", "\n"), re.S)
    if not m: return None
    head, body = m.group(1), m.group(2)
    n = re.search(r"^name:[ \t]*(.+)$", head, re.M)
    d = re.search(r"^description:[ \t]*(.*(?:\n[ \t]+.*)*)", head, re.M)
    desc = re.sub(r"^[>|][-+]?\s*", "", d.group(1).strip()) if d else ""
    return {"name": n.group(1).strip().strip("'\"") if n else "", "description": " ".join(desc.split()).strip("'\""),
            "body": "\n".join(l.rstrip() for l in body.strip("\n").split("\n")) + "\n"}
def norm(s):
    return hashlib.sha256("\0".join([s["name"], s["description"], s["body"].strip()]).encode()).hexdigest() if s else None
def live(i):
    p = os.path.join(LIVE, i, "SKILL.md")
    return parse(open(p, encoding="utf-8-sig", errors="replace").read()) if os.path.exists(p) else None
def release(sha, man, src=None):  # every skill of one published version, each checked against its manifest
    out = {}
    for s in man["skills"]:
        data = read(sha, s["path"], src)
        if hashlib.sha256(data).hexdigest() != s["sha256"]: sys.exit(f"HASH_MISMATCH {s['id']}")
        out[s["id"]] = parse(data.decode())
        if not out[s["id"]]: sys.exit(f"BAD_SKILL_FILE {s['id']}")
    return out
def published(version):  # earlier published copies per skill: the installed version's, or every one when it is unknown
    out, seen = {}, set()
    commits = [{"sha": None}] if OLD_SRC else json.loads(get(f"https://api.github.com/repos/{REPO}/commits?path={DIR}/manifest.json&per_page=50"))
    for c in commits:
        man = json.loads(read(c["sha"], "manifest.json", OLD_SRC))
        if version and man.get("version") != version: continue
        for s in man["skills"]:
            if s["sha256"] in seen: continue
            seen.add(s["sha256"]); data = read(c["sha"], s["path"], OLD_SRC)
            if hashlib.sha256(data).hexdigest() == s["sha256"]: out.setdefault(s["id"], []).append(parse(data.decode()))
        if version: break
    return out
def merge(l, b, n):  # the user's edit and the release's change in one copy, or None when they touch the same lines
    d = n["description"] if l["description"] == b["description"] else l["description"] if n["description"] == b["description"] else None
    if d is None: return None
    with tempfile.TemporaryDirectory() as t:
        paths = []
        for k, s in (("live", l), ("base", b), ("new", n)):
            paths.append(os.path.join(t, k)); open(paths[-1], "w").write(s["body"])
        try: r = subprocess.run(["git", "merge-file", "-p", *paths], capture_output=True, text=True)
        except FileNotFoundError: return None
    return {"name": n["name"], "description": d, "body": r.stdout} if r.returncode == 0 else None
def diff(a, b, x, y):
    r = subprocess.run(["git", "diff", "--no-index", "--no-color", "-U2", "--", a, b], capture_output=True, text=True)
    return r.stdout.replace("a" + a, x).replace("b" + b, y)
def plan():
    sha = "local" if SRC else json.loads(get(f"https://api.github.com/repos/{REPO}/commits/main"))["sha"]
    new = json.loads(read(sha, "manifest.json", SRC)); rel = release(sha, new, SRC)
    old = jload(os.path.join(STATE, "manifest.json"), {"version": None, "skills": []})
    kept = jload(os.path.join(STATE, "kept.json"), {}); has_live = os.path.isdir(LIVE)
    bases = {i: jload(os.path.join(STATE, "base", i + ".json"), None) for i in rel}
    earlier = published(old["version"]) if has_live and not all(bases.values()) else {}
    was = {s["id"]: s["sha256"] for s in old["skills"]}; now = {s["id"]: s["sha256"] for s in new["skills"]}
    st, merged = {}, {}
    for i, n in rel.items():
        if not has_live:
            st[i] = "same" if was.get(i) == now[i] else "changed"; continue
        l, c = live(i), earlier.get(i, [])
        b = bases[i] = bases[i] or next((x for x in c if norm(x) == norm(l)), None) or (c[0] if old["version"] and c else None)
        if l is None: st[i] = "new"
        elif norm(l) == norm(n): st[i] = "same"
        elif kept.get(i) == norm(n): st[i] = "kept"
        elif b is None: st[i] = "unknown"
        elif norm(l) == norm(b): st[i] = "changed"
        elif norm(n) == norm(b): st[i] = "yours"
        else:
            st[i] = "both"; m = merge(l, b, n)
            if m: merged[i] = m
    lives = sorted(d for d in os.listdir(LIVE) if d.startswith("ao-")) if has_live else []
    removed = sorted(set(lives) - set(rel)) or sorted({s["id"] for s in old["skills"]} - set(rel))
    return sha, new, old, rel, bases, st, merged, removed, has_live
cmd = sys.argv[1] if len(sys.argv) > 1 else "check"
if cmd == "check":
    sha, new, old, rel, bases, st, merged, removed, has_live = plan()
    pick = lambda k: [i for i in sorted(st) if st[i] == k]
    print(json.dumps({"installed": old["version"], "latest": new["version"], "commit": sha[:12], "live": has_live,
        "new": pick("new"), "changed": pick("changed"), "yours": pick("yours"), "kept": pick("kept"), "unknown": pick("unknown"),
        "both": [{"id": i, "merges_cleanly": i in merged} for i in pick("both")], "removed": removed}))
elif cmd == "fetch":
    sha, new, old, rel, bases, st, merged, removed, has_live = plan()
    shutil.rmtree(PENDING, ignore_errors=True); os.makedirs(PENDING)
    back = os.path.join(STATE, "backups", new["version"] + "-" + datetime.datetime.now().strftime("%Y%m%d-%H%M%S"))
    apply = sorted([i for i in st if st[i] in ("new", "changed")], key=lambda i: (i != "ao-rules", i == "ao-update", i))
    ask = []
    for i in rel:
        jsave(os.path.join(PENDING, i + ".new.json"), rel[i])
        if bases[i]: jsave(os.path.join(PENDING, i + ".base.json"), bases[i])
        if st[i] in ("changed", "both", "unknown") and has_live:
            os.makedirs(back, exist_ok=True); shutil.copy(os.path.join(LIVE, i, "SKILL.md"), os.path.join(back, i + ".md"))
        if st[i] in ("both", "unknown"):
            e = {"id": i, "status": st[i]}
            with tempfile.TemporaryDirectory() as t:
                w = lambda k, s: (open(os.path.join(t, k), "w").write(s["description"] + "\n\n" + s["body"]), os.path.join(t, k))[1]
                lp, np_ = w("live", live(i)), w("new", rel[i])
                if bases[i]:
                    bp = w("base", bases[i])
                    open(os.path.join(PENDING, i + ".yours.diff"), "w").write(diff(bp, lp, "installed/" + i, "yours/" + i)); e["your_edit"] = i + ".yours.diff"
                    open(os.path.join(PENDING, i + ".release.diff"), "w").write(diff(bp, np_, "installed/" + i, "new/" + i)); e["release_change"] = i + ".release.diff"
                else:
                    open(os.path.join(PENDING, i + ".differs.diff"), "w").write(diff(np_, lp, "new/" + i, "yours/" + i)); e["differs"] = i + ".differs.diff"
                if i in merged:
                    jsave(os.path.join(PENDING, i + ".merged.json"), merged[i]); e["keep_my_edit"] = i + ".merged.json"
                    mp = w("merged", merged[i])
                    open(os.path.join(PENDING, i + ".kept.diff"), "w").write(diff(np_, mp, "new/" + i, "with-your-edit/" + i))
            ask.append(e)
    jsave(os.path.join(PENDING, "PLAN.json"), {"version": new["version"], "commit": sha, "apply": apply, "ask_the_user": ask,
        "yours_kept": [i for i in st if st[i] in ("yours", "kept")], "removed": removed, "backups": back, "manifest": new})
    print(json.dumps({"apply": apply, "ask_the_user": [e["id"] for e in ask], "folder": PENDING}))
elif cmd == "done":
    p = jload(os.path.join(PENDING, "PLAN.json"), None) or sys.exit("NO_PLAN")
    synced = []
    for s in p["manifest"]["skills"]:
        i = s["id"]; n = jload(os.path.join(PENDING, i + ".new.json"), None); m = jload(os.path.join(PENDING, i + ".merged.json"), None)
        if n and (not os.path.isdir(LIVE) or norm(live(i)) in (norm(n), norm(m))):
            jsave(os.path.join(STATE, "base", i + ".json"), n); synced.append(i)
        elif os.path.exists(os.path.join(PENDING, i + ".base.json")):
            jsave(os.path.join(STATE, "base", i + ".json"), jload(os.path.join(PENDING, i + ".base.json"), None))
    jsave(os.path.join(STATE, "manifest.json"), p["manifest"]); print("INSTALLED", p["version"], "in step:", " ".join(synced))
elif cmd == "keep":
    i = sys.argv[2]; n = jload(os.path.join(PENDING, i + ".new.json"), None) or sys.exit("NO_PLAN")
    k = jload(os.path.join(STATE, "kept.json"), {}); k[i] = norm(n); jsave(os.path.join(STATE, "kept.json"), k); print("KEPT", i)
else:
    sys.exit("usage: fetch.py check|fetch|done|keep <id>")
PY
}; f
```

## 2. Check (read-only, no yes needed)
`python3 /workspace/ao-bot/fetch.py check` prints the installed and latest version and one list per kind:

| Kind | Meaning | Do |
|---|---|---|
| `new` | Not in the bot yet | Create it |
| `changed` | Only the release changed it | Update it |
| `yours` | The user edited it; the release didn't change it | Keep it. No question |
| `kept` | The user chose to keep their copy of this release | Keep it. No question |
| `both` | The user edited it and the release changed it. `merges_cleanly` says whether the two touch different lines | Ask, one by one ("Skills you edited") |
| `unknown` | It differs from the release and nothing on record says whether the user edited it | Ask, one by one |
| `removed` | The release no longer has it | Ask before deleting |

Nothing new, changed, both or unknown: say it's up to date and stop. `"live": false` means the box has no skills folder where Grok Bot keeps them, so edits can't be seen: say that updating replaces the changed skills as they are. `DOWNLOAD_FAILED`: GitHub or the network didn't answer (a 404 means the skills aren't published at that path yet). Say so and try again later.

## 3. Show and ask once
One short message: installed and latest version, the new and changed skills, the ones kept as the user's edit, and any `both`, `unknown` or removed ones. Ask "Update to <latest>?". "Yes, but skip <skill>": leave it out, and after step 4.1 run `python3 /workspace/ao-bot/fetch.py keep <skill>`.

## 4. On yes
1. `python3 /workspace/ao-bot/fetch.py fetch`. It downloads every skill of that commit, checks each sha256 against the manifest, backs up each live copy it may replace, and writes `/workspace/ao-bot/pending/`: `PLAN.json` plus `<id>.new.json` (`name`, `description`, `body`) per skill. `HASH_MISMATCH <id>`: stop, change nothing, and tell the user.
2. Go through `PLAN.json` `apply` in order (ao-rules first, ao-update last). For each, call UpdateSkill with that `<id>.new.json`'s `name`, `description` and `body`: rewrite the skill of that name, or create it. A subagent can't call UpdateSkill: the main agent does this step.
3. Handle `ask_the_user` ("Skills you edited").
4. `python3 /workspace/ao-bot/fetch.py done` records the version and, per skill now in step with the release, the copy later checks compare against.
5. For each removed skill, ask "Delete <ids>?" and delete only after a separate yes.
6. Do the "After an update" checks, reading them from this skill as it is now. Update "Orchestrator Bot version" in memory. Finish in one line: the version, and the skills updated, created and kept.

## Skills you edited
An update never replaces a user's edit on its own. Per skill in `ask_the_user`, one card. First show in two or three lines what the user changed (`<id>.yours.diff`) and in one line what the release changed (`<id>.release.diff`). For `unknown`, show how theirs differs from the new one (`<id>.differs.diff`). Then the choices:
1. **Keep mine**: save nothing, run `python3 /workspace/ao-bot/fetch.py keep <id>`. Say in one line what the release added that this skill will miss. Not offered for ao-update and ao-rules: they hold the update and safety rules every other skill relies on.
2. **Take the new one**: UpdateSkill with `<id>.new.json`. Say where the backup is (`backups` in `PLAN.json`).
3. **Update and keep my edit** (only when the entry has `keep_my_edit`): show where their lines ended up (`<id>.kept.diff`, two or three lines), then UpdateSkill with `<id>.merged.json`.
Never keep an edit that removes or weakens an ao-rules "Decisions", "Writes", "Secrets" or "data" rule: say so and offer choice 2.

"Reset <skill>": UpdateSkill with its `<id>.new.json` from a fresh `fetch`. "Restore my <skill>": UpdateSkill with the newest backup under `/workspace/ao-bot/backups/` that differs from the skill as it is now (frontmatter name and description, the rest as body).

## Update mode
One memory note says how updates happen: "Orchestrator Bot updates: auto", "notify" or "off". Setup asks once (ao-setup "Mode and routines"). "Update automatically", "just tell me about updates" or "stop checking for updates" changes the note and resumes or pauses the **Orchestrator Bot updates** routine (Mondays at 10:00) to match. Create the routine if it is missing, never a second one.
- **auto**: the weekly run installs a new version by itself and tells the user after. Skills the user edited are never replaced in that run.
- **notify**: the weekly run installs nothing. It tells the user a version is out and asks "Update to <latest>?". Their yes in chat runs step 4.
- **off**: the routine is paused. The user says "update Orchestrator Bot" when they want it.

## Weekly update run
The routine's prompt is: "Orchestrator Bot weekly update. Follow ao-update "Weekly update run" and ao-rules. Send nothing when nothing changed."
1. Save the fetcher if it differs (step 1), then run the check. Nothing new, changed, both or unknown: do the "After an update" checks if the memory note "Orchestrator Bot checks done for <version>" is missing or older, and otherwise send nothing.
2. **notify**: one message, once per version (note "update offered: <version>" in memory): the latest version, the new and changed skills, and any skill waiting for the user's choice. Ask "Update to <latest>?". Stop.
3. **auto**: run step 4.1, 4.2, 4.4 and 4.6. Leave `ask_the_user` skills as they are and list them with their three choices, for the main chat. Never delete a skill from a routine run.
4. One short message: the old and new version, what was updated, and what waits for the user.
5. `HASH_MISMATCH` or a failed step: say what was and wasn't updated, once per version and error, and note it in memory so later runs stay quiet until the version or the error changes.

## After an update
Bot-side things a new version may change. Do each after every update, then save the memory note "Orchestrator Bot checks done for <version>".

| Check | If so |
|---|---|
| A routine named **AO events to chat** exists and its prompt doesn't start with the first line of ao-live "Routine prompt" | Replace its prompt with that text (UpdateRoutine). The webhook URL and key stay, so nothing is pasted again |
| The **Orchestrator Bot updates** routine's prompt differs from "Weekly update run" | Replace it. Keep its schedule and whether it is on or paused |
| Memory has no "Orchestrator Bot updates" note and the routine exists | On means auto, paused means off. Don't ask; say once that "notify" exists |
| The **AO hosts** note has a host without `id` | Fill it in (ao-setup "Probe" prints `hostId`) |
