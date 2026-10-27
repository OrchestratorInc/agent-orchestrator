---
name: ao-update
description: >-
  Update this bot's ao-* skills to the latest published version: check what
  changed, show it, and after the user's yes download each changed skill,
  verify its sha256 against the published manifest, and rewrite it. Use for
  "update the AO bot", "update my AO skills", "what's new in the AO bot".
---
Follow the ao-rules skill. This runs on the bot's own cloud computer (the box), not on the user's computers. It changes only this bot's ao-* skills, and only after the user's yes.

The skills are published in the public AO repository (`OrchestratorInc/agent-orchestrator`, folder `integrations/grok-bot/`) with a `manifest.json` that lists each skill's sha256. Every check reads the newest commit on `main`, then downloads the manifest and the skills from that exact commit, so the files always match each other.

## 1. Save the fetcher
Save it on the box if `/workspace/ao-bot/fetch.py` is missing or differs:
```sh
f(){ mkdir -p /workspace/ao-bot && cat > /workspace/ao-bot/fetch.py <<'PY'
import hashlib, json, os, re, subprocess, sys
REPO, DIR = "OrchestratorInc/agent-orchestrator", "integrations/grok-bot"
SRC = os.environ.get("AO_BOT_SOURCE")  # a local folder instead of GitHub, for testing
STATE = os.environ.get("AO_BOT_STATE", "/workspace/ao-bot")
def get(url):  # curl, so the system's certificates are used
    r = subprocess.run(["curl", "-fsSL", "--max-time", "30", "-H", "User-Agent: ao-bot", url], capture_output=True)
    if r.returncode: sys.exit(f"DOWNLOAD_FAILED {url} {r.stderr.decode().strip()}")
    return r.stdout
def commit():
    return "local" if SRC else json.loads(get(f"https://api.github.com/repos/{REPO}/commits/main"))["sha"]
def read(sha, path):
    return open(os.path.join(SRC, path), "rb").read() if SRC else get(f"https://raw.githubusercontent.com/{REPO}/{sha}/{DIR}/{path}")
def installed():
    p = os.path.join(STATE, "manifest.json")
    return json.load(open(p)) if os.path.exists(p) else {"version": None, "skills": []}
def split(text):  # frontmatter name and folded description, then the body
    m = re.match(r"---\nname: (.+)\ndescription: >-\n((?:  .*\n)+)---\n", text)
    if not m: sys.exit("BAD_SKILL_FILE")
    return m.group(1).strip(), " ".join(l.strip() for l in m.group(2).splitlines()), text[m.end():]
cmd = sys.argv[1] if len(sys.argv) > 1 else "check"
sha = commit(); new = json.loads(read(sha, "manifest.json")); old = installed()
was = {s["id"]: s["sha256"] for s in old["skills"]}; now = {s["id"]: s for s in new["skills"]}
changed = [i for i in now if was.get(i) != now[i]["sha256"]]
removed = [i for i in was if i not in now]
if cmd == "check":
    print(json.dumps({"installed": old["version"], "latest": new["version"], "commit": sha[:12], "changed": changed, "removed": removed}))
elif cmd == "fetch":
    out = os.path.join(STATE, "pending"); files = {}
    for i in changed:
        data = read(sha, now[i]["path"])
        if hashlib.sha256(data).hexdigest() != now[i]["sha256"]: sys.exit(f"HASH_MISMATCH {i}")
        files[i] = data.decode()
    os.makedirs(out, exist_ok=True)
    for f in os.listdir(out): os.remove(os.path.join(out, f))
    order = sorted(files, key=lambda i: (i != "ao-rules", i == "ao-update", i))
    for i in order:
        name, desc, body = split(files[i])
        json.dump({"name": name, "description": desc, "body": body}, open(os.path.join(out, i + ".json"), "w"))
    json.dump({"version": new["version"], "commit": sha, "apply": order, "manifest": new}, open(os.path.join(out, "PLAN.json"), "w"))
    print(json.dumps({"ready": order, "folder": out}))
elif cmd == "done":
    plan = json.load(open(os.path.join(STATE, "pending", "PLAN.json")))
    json.dump(plan["manifest"], open(os.path.join(STATE, "manifest.json"), "w")); print("INSTALLED", plan["version"])
PY
}; f
```

## 2. Check (read-only, no yes needed)
`python3 /workspace/ao-bot/fetch.py check` prints the installed and latest version, the changed skills and any removed ones. `installed: null` means this bot was never updated here: every skill counts as changed, which is fine. Nothing changed: say it's up to date and stop. `DOWNLOAD_FAILED`: GitHub or the network didn't answer (a 404 means the skills aren't published at that path yet). Say so and try again later.

## 3. Show and ask once
One short message: installed and latest version, which skills changed, and any removed ones. Ask "Update to <latest>?".

## 4. On yes
1. `python3 /workspace/ao-bot/fetch.py fetch`. It downloads each changed skill, checks its sha256 against the manifest and, only if all match, writes one `<id>.json` per skill plus `PLAN.json` in `/workspace/ao-bot/pending/`. `HASH_MISMATCH <id>`: stop, change nothing, and tell the user.
2. Go through `PLAN.json` `apply` in order (ao-rules first, ao-update last). For each, call UpdateSkill with that json's `name`, `description` and `body`: rewrite the existing skill of that name, or create it if it doesn't exist. A subagent can't call UpdateSkill: the main agent does this step.
3. `python3 /workspace/ao-bot/fetch.py done` records the new version as installed.
4. For each removed skill, ask "Delete <ids>?" and delete only after a separate yes.
5. Update "AO bot version" in memory. Finish in one line: the version, and the skills updated, created and kept.
