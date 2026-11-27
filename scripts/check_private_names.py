#!/usr/bin/env python3
"""Fails when a tracked file names one of the control plane's private names.

The names are known here only by their SHA-256 (public-boundary-names.sha256),
so this public repository never spells them out. Every identifier-like token
in the tracked files is hashed whole and in parts: from its start or after a
"/" or ".", to its end or to (before or after) a "_", "." or "/". So a name
inside a longer token, or a name that is a prefix (such as a family of
settings), is caught.
A hit is reported by file and line only.
"""
import hashlib
import re
import subprocess
import sys

TOKEN = re.compile(r"[A-Za-z0-9_./-]{4,}")


def load_hashes(path):
    hashes = set()
    with open(path, encoding="utf-8") as listing:
        for line in listing:
            line = line.strip()
            if line and not line.startswith("#"):
                hashes.add(line.lower())
    return hashes


def candidates(token):
    """The token, and its parts that start it or follow a "/" or ".", and end
    it or end at (before or after) a "_", "." or "/"."""
    starts = {0} | {i + 1 for i, c in enumerate(token) if c in "/."}
    ends = {len(token)}
    for i, c in enumerate(token):
        if c in "_./":
            ends.update((i, i + 1))
    for start in starts:
        for end in ends:
            if end - start >= 4:
                yield token[start:end]


def main():
    hash_file = sys.argv[1]
    hashes = load_hashes(hash_file)
    files = subprocess.run(["git", "ls-files", "-z"], check=True, capture_output=True).stdout.split(b"\0")
    hits = []
    seen = {}
    for name in files:
        if not name or name.decode() == hash_file:
            continue
        try:
            with open(name, "rb") as handle:
                data = handle.read()
        except OSError:
            continue
        if b"\0" in data[:8192]:
            continue  # binary
        text = data.decode("utf-8", "replace")
        for number, line in enumerate(text.splitlines(), 1):
            for token in TOKEN.findall(line):
                matched = seen.get(token)
                if matched is None:
                    matched = any(hashlib.sha256(c.encode()).hexdigest() in hashes for c in candidates(token))
                    seen[token] = matched
                if matched:
                    hits.append(f"{name.decode()}:{number}")
                    break
    for hit in hits:
        print(hit)
    return 1 if hits else 0


if __name__ == "__main__":
    sys.exit(main())
