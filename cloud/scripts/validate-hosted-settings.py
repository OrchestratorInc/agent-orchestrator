#!/usr/bin/env python3
"""Validate hosted deployment settings before anything is built or registered.

Three modes, one per invocation:

  --nodeops|--coder|--freestyle FILE --worker FILE   provider and worker secrets
  --stripe FILE --environment ENV                    the optional Stripe secret
  --offering --environment ENV ...                   the offering settings
"""

import argparse
import json
from pathlib import Path

from lib.deployment import (
    csv_list,
    validate_hosted_settings,
    validate_offering,
    validate_stripe_settings,
)


def load_secret(path: str) -> dict[str, object]:
    value = json.loads(Path(path).read_text())
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a JSON object")
    return value


def main() -> None:
    parser = argparse.ArgumentParser()
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--nodeops")
    mode.add_argument("--coder")
    mode.add_argument("--freestyle")
    mode.add_argument("--stripe")
    mode.add_argument("--offering", action="store_true")
    parser.add_argument("--worker")
    parser.add_argument("--environment", choices=("staging", "production"))
    offering = parser.add_argument_group("offering")
    offering.add_argument("--providers", default="")
    offering.add_argument("--provider", default="")
    offering.add_argument(
        "--staging-providers",
        help="Production only: the providers the healthy staging task serves.",
    )
    offering.add_argument("--capability-gated-providers", default="")
    offering.add_argument("--default-org-capabilities", default="")
    offering.add_argument("--default-org-billing", default="")
    offering.add_argument("--paywalled-features", default="")
    offering.add_argument("--idle-pause-threshold", default="")
    offering.add_argument("--freestyle-idle-pause-threshold", default="")
    offering.add_argument("--artifacts", default="0")
    offering.add_argument("--artifact-bucket", default="")
    offering.add_argument("--artifact-region", default="")
    offering.add_argument("--coder-readable-names", default="")
    offering.add_argument("--coder-worker-working-folder", default="")
    args = parser.parse_args()

    if args.stripe:
        if not args.environment:
            parser.error("--stripe requires --environment")
        validate_stripe_settings(load_secret(args.stripe), environment=args.environment)
        return
    if args.offering:
        if not args.environment:
            parser.error("--offering requires --environment")
        staging_providers = None
        if args.environment == "production":
            if args.staging_providers is None:
                parser.error("--offering --environment production requires --staging-providers")
            staging_providers = csv_list(args.staging_providers)
        validate_offering(
            providers=csv_list(args.providers),
            default_provider=args.provider,
            gated_providers=csv_list(args.capability_gated_providers),
            default_org_capabilities=csv_list(args.default_org_capabilities),
            default_org_billing=args.default_org_billing,
            paywalled_features=csv_list(args.paywalled_features),
            idle_pause_threshold=args.idle_pause_threshold,
            freestyle_idle_pause_threshold=args.freestyle_idle_pause_threshold,
            coder_readable_names=args.coder_readable_names,
            coder_worker_working_folder=args.coder_worker_working_folder,
            staging_providers=staging_providers,
            artifacts=args.artifacts,
            artifact_bucket=args.artifact_bucket,
            artifact_region=args.artifact_region,
        )
        return

    if not args.worker:
        parser.error("provider validation requires --worker")
    if args.freestyle:
        provider_name, provider_path = "freestyle", args.freestyle
    elif args.coder:
        provider_name, provider_path = "coder", args.coder
    else:
        provider_name, provider_path = "nodeops", args.nodeops
    validate_hosted_settings(
        load_secret(provider_path),
        load_secret(args.worker),
        provider=provider_name,
    )


if __name__ == "__main__":
    try:
        main()
    except ValueError as error:
        raise SystemExit(f"invalid hosted settings: {error}") from error
