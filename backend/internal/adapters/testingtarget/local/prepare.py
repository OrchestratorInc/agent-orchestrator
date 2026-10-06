#!/usr/bin/env python3
"""Prepare a dedicated, unpackaged target checkout without inherited AO state."""

import os
from pathlib import Path
import subprocess

adapter = Path(__file__).resolve().parent
repository = adapter.parents[4]
checkout = Path.home() / ".ao/dev/agentic-target/checkout"
env = {key: value for key, value in os.environ.items() if not key.startswith("AO_")}
env.update(GOFLAGS="-p=2", GOMAXPROCS="2", npm_config_jobs="2", MAKEFLAGS="-j2")


def run(arguments, cwd):
    subprocess.run(arguments, cwd=cwd, env=env, check=True)


if checkout.exists():
    raise SystemExit(f"Refusing to reuse an existing checkout: {checkout}")
checkout.parent.mkdir(parents=True, exist_ok=True)
run(["git", "worktree", "add", "--detach", str(checkout), "feat/agentic-testing"], repository)
for directory in ["frontend", "packages/product-ui"]:
    run(["npm", "ci"], checkout / directory)
run(["npm", "run", "build:daemon", "--", "--dev"], checkout / "frontend")
run(["node", str(adapter / "prepare.cjs"), str(checkout / "frontend")], checkout / "frontend")
print(f"Prepared target checkout: {checkout}")
