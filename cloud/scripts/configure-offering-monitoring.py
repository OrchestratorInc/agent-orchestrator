#!/usr/bin/env python3
"""Render (default) or apply the AO Cloud offering metric filters, alarms and dashboard.

    # Review what would be applied, or regenerate cloud/infra/monitoring/*.json:
    python3 cloud/scripts/configure-offering-monitoring.py --environment staging
    python3 cloud/scripts/configure-offering-monitoring.py --environment production \\
        --write-dir cloud/infra/monitoring

    # Print the equivalent aws CLI commands (nothing is executed):
    python3 cloud/scripts/configure-offering-monitoring.py --environment production --print-commands

    # Apply with the aws CLI (needs logs:PutMetricFilter, cloudwatch:PutMetricAlarm,
    # cloudwatch:PutDashboard):
    AO_CLOUD_ALERT_TOPIC_ARN=arn:aws:sns:... \\
        python3 cloud/scripts/configure-offering-monitoring.py --environment production --apply
"""

import argparse
import json
import os
import shlex
import subprocess
from pathlib import Path

from lib.offering_monitoring import alarm_definitions, dashboard_body, metric_filters


def aws_commands(region: str, environment: str, operations: dict) -> list[list[str]]:
    commands = []
    for definition in operations["metricFilters"]:
        commands.append([
            "aws", "--region", region, "logs", "put-metric-filter",
            "--log-group-name", definition["logGroupName"],
            "--filter-name", definition["filterName"],
            "--filter-pattern", definition["filterPattern"],
            "--metric-transformations", json.dumps(definition["metricTransformations"], separators=(",", ":")),
        ])
    for alarm in operations["alarms"]:
        commands.append([
            "aws", "--region", region, "cloudwatch", "put-metric-alarm",
            "--cli-input-json", json.dumps(alarm, separators=(",", ":")),
        ])
    commands.append([
        "aws", "--region", region, "cloudwatch", "put-dashboard",
        "--dashboard-name", f"ao-cloud-offering-{environment}",
        "--dashboard-body", json.dumps(operations["dashboard"], separators=(",", ":")),
    ])
    return commands


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--region", default=os.environ.get("AWS_REGION", "eu-north-1"))
    parser.add_argument("--environment", choices=("staging", "production"), required=True)
    parser.add_argument("--write-dir", help="write the definitions as JSON files into this directory")
    parser.add_argument("--print-commands", action="store_true", help="print the aws CLI commands")
    parser.add_argument("--apply", action="store_true", help="run the aws CLI commands")
    args = parser.parse_args()
    topic = os.environ.get("AO_CLOUD_ALERT_TOPIC_ARN", "").strip()
    operations = {
        "metricFilters": metric_filters(args.environment),
        "alarms": alarm_definitions(args.environment, topic),
        "dashboard": dashboard_body(args.region, args.environment),
    }
    if args.write_dir:
        directory = Path(args.write_dir)
        directory.mkdir(parents=True, exist_ok=True)
        for name, value in operations.items():
            path = directory / f"offering-{args.environment}-{name}.json"
            path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
            print(f"wrote {path}")
    commands = aws_commands(args.region, args.environment, operations)
    profile = os.environ.get("AWS_PROFILE", "").strip()
    if profile:
        commands = [[command[0], "--profile", profile, *command[1:]] for command in commands]
    if args.print_commands:
        for command in commands:
            print(shlex.join(command))
    if args.apply:
        for command in commands:
            subprocess.run(command, check=True, capture_output=True, text=True)
        print(
            f"Configured {len(operations['metricFilters'])} metric filters, {len(operations['alarms'])} alarms"
            f" and the ao-cloud-offering-{args.environment} dashboard."
        )
        if not topic:
            print("AO_CLOUD_ALERT_TOPIC_ARN is unset; alarms are visible but have no notification actions.")
    if not (args.write_dir or args.print_commands or args.apply):
        print(json.dumps(operations, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
