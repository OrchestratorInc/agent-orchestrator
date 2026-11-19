#!/usr/bin/env python3
"""Write manifest.json for the AO Grok Bot skills: version plus sha256 per skill.

Run after any skill change: python3 make-manifest.py <version>   (for example 2026.10.27.1)
"""
import hashlib, json, os, sys

here = os.path.dirname(os.path.abspath(__file__))
if len(sys.argv) != 2:
    sys.exit("usage: make-manifest.py <version>")
skills = []
for sid in sorted(os.listdir(os.path.join(here, "skills"))):
    path = f"skills/{sid}/SKILL.md"
    data = open(os.path.join(here, path), "rb").read()
    skills.append({"id": sid, "path": path, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest()})
json.dump({"name": "ao-grok-bot", "version": sys.argv[1], "skills": skills},
          open(os.path.join(here, "manifest.json"), "w"), indent=1)
print(f"manifest.json: {sys.argv[1]}, {len(skills)} skills")
