#!/usr/bin/env python3
"""Print the Freestyle harness snapshot ids that are safe to delete.

Reads the Freestyle GET /v5/snapshots listing on stdin and prints one id per
line. Snapshots named by any --protect-task-definition file (ECS
describe-task-definition output) or any --protect id are never printed, nor is
the newest --keep set per harness.
"""

import argparse
import json
import sys
from pathlib import Path

from lib.deployment import csv_list, stale_harness_snapshots, task_definition_freestyle_snapshots


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--harnesses", required=True)
    parser.add_argument("--keep", type=int, default=2)
    parser.add_argument("--protect", action="append", default=[])
    parser.add_argument("--protect-task-definition", action="append", default=[])
    args = parser.parse_args()
    if args.keep < 1:
        parser.error("--keep must be at least 1")
    protected = {snapshot for value in args.protect for snapshot in csv_list(value)}
    for path in args.protect_task_definition:
        protected |= task_definition_freestyle_snapshots(json.loads(Path(path).read_text()))
    listing = json.load(sys.stdin)
    for snapshot in stale_harness_snapshots(
        listing, csv_list(args.harnesses), protected, keep=args.keep
    ):
        print(snapshot)


if __name__ == "__main__":
    main()
