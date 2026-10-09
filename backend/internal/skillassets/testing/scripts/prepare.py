#!/usr/bin/env python3
"""Prepare a reusable local target, keeping dependencies and normal caches."""

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import os
from pathlib import Path
import subprocess


def run(arguments, cwd):
    env = {key: value for key, value in os.environ.items() if not key.startswith("AO_")}
    return subprocess.run(arguments, cwd=cwd, env=env, check=True,
                          text=True, stdout=subprocess.PIPE).stdout.strip()


@contextmanager
def reserve(checkout):
    path = checkout / ".ao-testing-active"
    with path.open("x") as lease:
        try:
            yield
        finally:
            if not os.path.samestat(os.fstat(lease.fileno()), path.lstat()):
                raise RuntimeError("Target checkout reservation changed; leaving it untouched")
            path.unlink()


def install(directory, stamp):
    lockfile = directory / "package-lock.json"
    digest = hashlib.sha256(lockfile.read_bytes()).hexdigest()
    if stamp.exists() and stamp.read_text().strip() == digest:
        return
    run(["npm", "install", "--prefer-offline", "--no-save", "--no-audit", "--no-fund"], directory)
    stamp.write_text(hashlib.sha256(lockfile.read_bytes()).hexdigest() + "\n")


def prepare(repository, commit, cache):
    repository = repository.resolve()
    try:
        origin = run(["git", "remote", "get-url", "origin"], repository)
    except subprocess.CalledProcessError:
        origin = run(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"], repository)
    root = cache / hashlib.sha256(origin.encode()).hexdigest()[:16]
    root.mkdir(parents=True, exist_ok=True)
    if root.resolve() != root:
        raise RuntimeError("Target cache must not contain symlinks")
    checkout = root / "checkout"
    with (root / "prepare.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if not checkout.exists():
            run(["git", "clone", "--no-checkout", "--reference-if-able", str(repository),
                 "--dissociate", origin, str(checkout)], root)
        else:
            if checkout.is_symlink():
                raise RuntimeError("Target checkout must not be a symlink")
            if run(["git", "remote", "get-url", "origin"], checkout) != origin:
                raise RuntimeError("Target checkout belongs to a different repository")
            if run(["git", "status", "--porcelain", "--untracked-files=no"], checkout):
                raise RuntimeError("Target checkout has tracked edits; preserve them before preparing")
        with reserve(checkout):
            run(["git", "fetch", "--no-tags", "origin"], checkout)
            # Admit local, unpushed revisions without changing the cached remote.
            source = "origin" if repository == checkout else str(repository)
            run(["git", "fetch", "--no-tags", source, commit], checkout)
            run(["git", "checkout", "--detach", commit], checkout)
            for directory in ["frontend", "packages/product-ui"]:
                install(checkout / directory, root / (directory.replace("/", "-") + ".sha256"))
            frontend = checkout / "frontend"
            adapter = Path(__file__).resolve().parent
            # The existing runtime builder skips unchanged source signatures.
            run(["node", "scripts/build-acp-runtime.mjs"], frontend)
            run(["node", "scripts/build-daemon.mjs", "--dev"], frontend)
            run(["node", str(adapter / "prepare.cjs"), str(frontend)], frontend)
    return checkout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", type=Path, default=Path(__file__).resolve().parents[5])
    parser.add_argument("--commit", help="Exact base or head commit; defaults to repository HEAD")
    args = parser.parse_args()
    commit = args.commit or run(["git", "rev-parse", "HEAD"], args.repository)
    if len(commit) not in (40, 64) or any(c not in "0123456789abcdef" for c in commit):
        parser.error("--commit must be a full lowercase commit SHA")
    checkout = prepare(args.repository, commit,
                       Path.home() / ".ao/dev/agentic-target/repos")
    print(f"Prepared target checkout: {checkout}")


if __name__ == "__main__":
    main()
