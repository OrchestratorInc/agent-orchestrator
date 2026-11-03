#!/usr/bin/env python3
"""Validate area references and first-match coverage using read-only Git objects."""
import argparse
import fnmatch
import json
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT.parents[2]
SOURCE_EXTENSIONS = set(".go .ts .tsx .js .jsx .mjs .cjs .mts .py .sh .sql .css .html .swift .c .h .cc .cpp .m .mm .java .kt .nix .tf .gradle .ps1 .bat .cmd".split())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=SOURCE)
    parser.add_argument("--ref", default="origin/main")
    args = parser.parse_args()
    taxonomy = json.loads((ROOT / "data/issue-areas.json").read_text())
    rules = json.loads((ROOT / "data/path-areas.json").read_text())
    ids = [a["id"] for a in taxonomy["areas"]]
    assert len(ids) == len(set(ids)), "duplicate taxonomy IDs"
    assert all(a.get("label") and a.get("description") for a in taxonomy["areas"])
    assert rules and rules[-1] == {"glob": "*", "area": "shared-infra"}
    assert all(set(r) == {"glob", "area"} and r["glob"] and r["area"] in ids for r in rules), "invalid path rule"
    assert all(i["area"] in ids for i in taxonomy["issues"].values()), "unknown issue area"
    git = lambda *a: subprocess.check_output(["git", *a], cwd=args.repo)
    revision = git("show", "-s", "--format=%H", args.ref).decode().strip()
    entries = git("ls-tree", "-r", "-z", args.ref).split(b"\0")
    paths = [entry.split(b"\t", 1)[1].decode() for entry in entries if entry]
    dead_globs = [rule["glob"] for rule in rules[:-1]
                  if not any(fnmatch.fnmatchcase(path, rule["glob"]) for path in paths)]
    assert not dead_globs, f"non-fallback globs match no tracked paths on {args.ref}: {', '.join(dead_globs)}"
    total = real = source = source_real = lines = lines_real = 0
    for entry in filter(None, entries):
        meta, raw_path = entry.split(b"\t", 1)
        path = raw_path.decode()
        matched = next((r for r in rules if fnmatch.fnmatchcase(path, r["glob"])), None)
        assert matched, f"unmapped path: {path}"
        nonfallback = matched["area"] != "shared-infra"
        total += 1
        real += nonfallback
        p = Path(path)
        is_source = p.suffix in SOURCE_EXTENSIONS or "Dockerfile" in p.name or p.name in {".envrc", "browser"}
        if meta.split()[1] != b"blob" or not is_source:
            continue
        content = git("show", f"{args.ref}:{path}")
        count = content.count(b"\n") + bool(content and not content.endswith(b"\n"))
        source += 1
        source_real += nonfallback
        lines += count
        lines_real += count * nonfallback
    assert source and source_real / source > .95, "non-fallback source-file coverage must exceed 95%"
    print(f"PASS: {len(ids)} areas; {len(rules)} rules; {len(taxonomy['issues'])} issue references valid")
    print(f"ref: {args.ref} {revision}")
    print(f"tracked paths: {total}/{total} (100.00%) matched; non-fallback {real}/{total} ({real/total:.2%})")
    print(f"source files: non-fallback {source_real}/{source} ({source_real/source:.2%})")
    print(f"source lines: non-fallback {lines_real}/{lines} ({lines_real/lines:.4%})")
    print("source lines include blanks, comments, tests and generated code; gitlinks have no source lines")


if __name__ == "__main__":
    main()
