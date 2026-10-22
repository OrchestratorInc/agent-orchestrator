#!/usr/bin/env python3
"""Pure helpers for producing and validating AO Cloud ECS deployments."""

from __future__ import annotations

import copy
import json
import posixpath
import re
from datetime import datetime
from typing import Any
from urllib.parse import urlparse


TASK_KEYS = {
    "networkMode",
    "containerDefinitions",
    "volumes",
    "placementConstraints",
    "requiresCompatibilities",
    "cpu",
    "memory",
    "pidMode",
    "ipcMode",
    "proxyConfiguration",
    "inferenceAccelerators",
    "ephemeralStorage",
    "runtimePlatform",
}

NODEOPS_SECRET_ENV = {
    "AO_CLOUD_NODEOPS_BASE_URL": "base_url",
    "AO_CLOUD_NODEOPS_API_KEY": "api_key",
    "AO_CLOUD_NODEOPS_DEFAULT_SHAPE": "default_shape",
    "AO_CLOUD_NODEOPS_DEFAULT_ROOTFS": "default_rootfs",
    "AO_CLOUD_NODEOPS_INGRESS": "ingress",
    "AO_CLOUD_NODEOPS_SSH_KEY_PATH": "ssh_key_path",
    "AO_CLOUD_NODEOPS_REGION": "region",
    "AO_CLOUD_NODEOPS_WORKER_TOKEN_TTL": "worker_token_ttl",
}
CODER_SECRET_ENV = {
    "AO_CLOUD_CODER_URL": "url",
    "AO_CLOUD_CODER_TOKEN": "token",
    "AO_CLOUD_CODER_OWNER": "owner",
    "AO_CLOUD_CODER_TEMPLATE_ID": "template_id",
    "AO_CLOUD_CODER_AGENT_NAME": "agent_name",
    "AO_CLOUD_CODER_PARAMETERS_JSON": "parameters_json",
    "AO_CLOUD_CODER_DURABLE_ROOT": "durable_root",
    "AO_CLOUD_CODER_WORKER_TOKEN_TTL": "worker_token_ttl",
}
FREESTYLE_SECRET_ENV = {
    "AO_CLOUD_FREESTYLE_API_KEY": "api_key",
    "AO_CLOUD_FREESTYLE_DEFAULT_SNAPSHOT": "default_snapshot",
    "AO_CLOUD_FREESTYLE_WORKER_TOKEN_TTL": "worker_token_ttl",
}
# Every harness a hosted Freestyle deployment serves needs its own baked
# snapshot (scripts/publish-freestyle-snapshot.sh).
FREESTYLE_HARNESSES = ("claude-code", "codex", "cursor")
WORKER_SECRET_ENV = {
    "AO_CLOUD_WORKER_SIGNING_KEY": "signing_key",
    "AO_CLOUD_MAX_ACTIVE_SANDBOXES_PER_ORG": "max_active_sandboxes_per_org",
    "AO_CLOUD_SANDBOX_RECONCILE_INTERVAL": "sandbox_reconcile_interval",
    "AO_CLOUD_SANDBOX_STARTUP_TIMEOUT": "sandbox_startup_timeout",
    "AO_CLOUD_WORKER_HEARTBEAT_TIMEOUT": "worker_heartbeat_timeout",
}
PROVIDER_SECRET_ENV = {
    "nodeops": NODEOPS_SECRET_ENV,
    "coder": CODER_SECRET_ENV,
    "freestyle": FREESTYLE_SECRET_ENV,
}
NODEOPS_PLAINTEXT_ENV = {"AO_CLOUD_NODEOPS_ROOTFS_BY_HARNESS"}
FREESTYLE_PLAINTEXT_ENV = {"AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS"}
PROVIDER_ENV_NAMES = (
    set(NODEOPS_SECRET_ENV)
    | set(CODER_SECRET_ENV)
    | set(FREESTYLE_SECRET_ENV)
    | NODEOPS_PLAINTEXT_ENV
    | FREESTYLE_PLAINTEXT_ENV
)
# Stripe billing is optional. The secret holds the key and webhook secret; the
# price ids, portal configuration, and grandfathering cutoff are not credentials
# and ride as plaintext.
STRIPE_SECRET_ENV = {
    "AO_CLOUD_STRIPE_SECRET_KEY": "secret_key",
    "AO_CLOUD_STRIPE_WEBHOOK_SECRET": "webhook_secret",
}
STRIPE_PLAINTEXT_ENV = {
    "AO_CLOUD_STRIPE_PRICE_IDS",
    "AO_CLOUD_STRIPE_PORTAL_CONFIGURATION",
    "AO_CLOUD_BILLING_EXEMPT_ORGS_CREATED_BEFORE",
}
# The operator support lookup token is optional (ao-cloud/<environment>/support,
# key "token"); without it the support endpoint does not exist.
SUPPORT_SECRET_ENV = {"AO_CLOUD_SUPPORT_TOKEN": "token"}
# AO_CLOUD_SANDBOX_DEFAULT_PROVIDER is a leftover no release reads; inherited,
# it only misleads whoever reads the task definition.
LEGACY_ENV = {"AO_CLOUD_SANDBOX_DEFAULT_PROVIDER"}
# Names a deploy sets explicitly or not at all. A value inherited from the
# previous revision is dropped, so deleting the Stripe secret turns billing off
# instead of leaving a stale secret reference behind.
DEPLOY_MANAGED_ENV = set(STRIPE_SECRET_ENV) | STRIPE_PLAINTEXT_ENV | set(SUPPORT_SECRET_ENV) | LEGACY_ENV
# The offering: which providers an organization may use and how it is billed.
# Every deploy sets these explicitly (scripts/offering/<environment>.env).
OFFERING_ENV = (
    "AO_CLOUD_CAPABILITY_GATED_PROVIDERS",
    "AO_CLOUD_DEFAULT_ORG_CAPABILITIES",
    "AO_CLOUD_DEFAULT_ORG_BILLING",
    "AO_CLOUD_PAYWALLED_FEATURES",
    "AO_CLOUD_IDLE_PAUSE_THRESHOLD",
    "AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD",
    "AO_CLOUD_CODER_READABLE_NAMES",
    "AO_CLOUD_CODER_WORKER_WORKING_FOLDER",
)
# Coder rollout flags: true or false, never anything the control plane would
# quietly read as its default (docs/deployment.md, "Coder rollout flags").
CODER_ROLLOUT_FLAGS = ("AO_CLOUD_CODER_READABLE_NAMES", "AO_CLOUD_CODER_WORKER_WORKING_FOLDER")
ORG_BILLING_MODES = ("self_serve", "contract")
PROVIDER_AUTO_PAUSE_ENV = "AO_CLOUD_NODEOPS_AUTO_PAUSE_MINUTES"
# Freestyle's own idle pause is not reported as an idle stop, so the reconciler
# would resume it at once; the control plane's idle scanner owns pausing.
FREESTYLE_AUTO_PAUSE_ENV = "AO_CLOUD_FREESTYLE_AUTO_PAUSE_SECONDS"
WORKER_BINARY_PATH = "/ao-worker"
WORKER_HELPER_BINARY_PATH = "/ao"
_DIGEST_IMAGE = re.compile(r"^.+@sha256:[0-9a-f]{64}$")
_DURATION_PART = re.compile(r"(\d+)(ms|s|m|h)")
_TOKEN = re.compile(r"^[a-z0-9][a-z0-9_.-]*$")


def resolve_sandbox_providers(
    sandbox_provider: str, sandbox_providers: list[str] | None
) -> list[str]:
    """The full set of sandbox providers a control-plane task serves.

    A single-provider deployment passes sandbox_providers=None and gets exactly
    [sandbox_provider], preserving the historical behavior. A multi-provider
    deployment (for example nodeops,coder) passes every provider it offers so
    all of their secrets are plumbed and preserved, and the primary
    (sandbox_provider) must be one of them.
    """
    providers = list(sandbox_providers) if sandbox_providers else [sandbox_provider]
    seen: list[str] = []
    for provider in providers:
        if provider not in PROVIDER_SECRET_ENV:
            raise ValueError(f"unsupported hosted sandbox provider: {provider}")
        if provider not in seen:
            seen.append(provider)
    if sandbox_provider not in seen:
        raise ValueError(
            "primary sandbox provider "
            f"{sandbox_provider!r} must be one of the available providers "
            f"{seen}"
        )
    return seen


def _inactive_provider_env_names(providers: list[str]) -> set[str]:
    """Provider env/secret names to prune: those of providers NOT in the set.

    Names belonging to an available provider are kept so a multi-provider task
    retains, for example, its coder secrets during a nodeops-primary deploy.
    """
    keep: set[str] = set()
    for provider in providers:
        keep |= set(PROVIDER_SECRET_ENV[provider])
    if "nodeops" in providers:
        keep |= NODEOPS_PLAINTEXT_ENV
    if "freestyle" in providers:
        keep |= FREESTYLE_PLAINTEXT_ENV
    return PROVIDER_ENV_NAMES - keep


def _required_provider_secrets(providers: list[str]) -> set[str]:
    required: set[str] = set()
    for provider in providers:
        required |= set(PROVIDER_SECRET_ENV[provider])
    return required


def secret_environment(secret_arn: str, fields: dict[str, str]) -> dict[str, str]:
    if not secret_arn.strip():
        raise ValueError("secret ARN must not be empty")
    return {
        environment_name: f"{secret_arn}:{json_key}::"
        for environment_name, json_key in fields.items()
    }


def validate_hosted_settings(
    provider_settings: dict[str, Any],
    worker: dict[str, Any],
    *,
    provider: str = "nodeops",
) -> None:
    if provider not in PROVIDER_SECRET_ENV:
        raise ValueError(f"unsupported hosted sandbox provider: {provider}")
    if provider == "nodeops":
        _validate_nodeops_settings(provider_settings)
    elif provider == "freestyle":
        _validate_freestyle_settings(provider_settings)
    else:
        _validate_coder_settings(provider_settings)
    _require_secret_strings("worker", worker, WORKER_SECRET_ENV.values())
    if len(worker["signing_key"].strip()) < 32:
        raise ValueError("worker signing_key must contain at least 32 characters")
    try:
        quota = int(worker["max_active_sandboxes_per_org"])
    except ValueError as error:
        raise ValueError(
            "worker max_active_sandboxes_per_org must be an integer"
        ) from error
    if quota < 1:
        raise ValueError("worker max_active_sandboxes_per_org must be positive")
    if _duration_seconds(worker["sandbox_reconcile_interval"]) <= 0:
        raise ValueError("worker sandbox_reconcile_interval must be positive")
    if _duration_seconds(worker["sandbox_startup_timeout"]) < 30:
        raise ValueError("worker sandbox_startup_timeout must be at least 30s")
    if _duration_seconds(worker["worker_heartbeat_timeout"]) < 30:
        raise ValueError("worker worker_heartbeat_timeout must be at least 30s")


def _validate_nodeops_settings(nodeops: dict[str, Any]) -> None:
    _require_secret_strings("NodeOps", nodeops, NODEOPS_SECRET_ENV.values())
    if "auto_pause_minutes" in nodeops:
        raise ValueError("NodeOps settings must not configure provider auto-pause")

    base_url = urlparse(nodeops["base_url"])
    if base_url.scheme != "https" or not base_url.netloc:
        raise ValueError("NodeOps base_url must be an absolute HTTPS URL")
    for key in ("api_key", "default_shape", "default_rootfs"):
        if not nodeops[key].strip():
            raise ValueError(f"NodeOps {key} must not be empty")
    if nodeops["ingress"].strip().lower() not in ("", "enabled", "disabled"):
        raise ValueError("NodeOps ingress must be enabled, disabled, or empty")
    if _duration_seconds(nodeops["worker_token_ttl"]) <= 0:
        raise ValueError("NodeOps worker_token_ttl must be positive")


def _validate_freestyle_settings(freestyle: dict[str, Any]) -> None:
    _require_secret_strings("Freestyle", freestyle, FREESTYLE_SECRET_ENV.values())
    if "auto_pause_seconds" in freestyle:
        raise ValueError("Freestyle settings must not configure provider auto-pause")
    for key in ("api_key", "default_snapshot"):
        if not freestyle[key].strip():
            raise ValueError(f"Freestyle {key} must not be empty")
    if _duration_seconds(freestyle["worker_token_ttl"]) <= 0:
        raise ValueError("Freestyle worker_token_ttl must be positive")
    snapshots = freestyle_snapshot_by_harness(freestyle)
    missing = [harness for harness in FREESTYLE_HARNESSES if harness not in snapshots]
    if missing:
        raise ValueError(
            "Freestyle snapshot_by_harness is missing: " + ", ".join(missing)
        )


def freestyle_snapshot_by_harness(freestyle: dict[str, Any]) -> dict[str, str]:
    """The harness-to-snapshot map, stored in the secret as a JSON string."""
    raw = freestyle.get("snapshot_by_harness", "")
    if not isinstance(raw, str) or not raw.strip():
        raise ValueError("Freestyle snapshot_by_harness must be a JSON object string")
    try:
        snapshots = json.loads(raw)
    except json.JSONDecodeError as error:
        raise ValueError("Freestyle snapshot_by_harness must be valid JSON") from error
    if not isinstance(snapshots, dict) or any(
        not isinstance(key, str) or not isinstance(value, str) or not value.strip()
        for key, value in snapshots.items()
    ):
        raise ValueError(
            "Freestyle snapshot_by_harness must map harness names to snapshot ids"
        )
    return snapshots


def _validate_coder_settings(coder: dict[str, Any]) -> None:
    _require_secret_strings("Coder", coder, CODER_SECRET_ENV.values())
    base_url = urlparse(coder["url"])
    if (
        base_url.scheme != "https"
        or not base_url.netloc
        or base_url.username is not None
        or base_url.path not in ("", "/")
        or base_url.query
        or base_url.fragment
    ):
        raise ValueError("Coder url must be an absolute HTTPS origin")
    for key in ("token", "owner", "template_id", "durable_root"):
        if not coder[key].strip():
            raise ValueError(f"Coder {key} must not be empty")
    durable_root = coder["durable_root"]
    if (
        len(durable_root) > 1024
        or not durable_root.startswith("/")
        or durable_root == "/"
        or posixpath.normpath(durable_root) != durable_root
        or any(ord(character) < 32 or ord(character) == 127 for character in durable_root)
    ):
        raise ValueError("Coder durable_root must be a safe absolute non-root path")
    try:
        parameters = json.loads(coder["parameters_json"])
    except json.JSONDecodeError as error:
        raise ValueError("Coder parameters_json must be valid JSON") from error
    if not isinstance(parameters, dict) or any(
        not isinstance(key, str) or not isinstance(value, str)
        for key, value in parameters.items()
    ):
        raise ValueError("Coder parameters_json must be an object of string values")
    if _duration_seconds(coder["worker_token_ttl"]) <= 0:
        raise ValueError("Coder worker_token_ttl must be positive")


def _require_secret_strings(
    label: str, values: dict[str, Any], required: Any
) -> None:
    missing = sorted(set(required) - values.keys())
    if missing:
        raise ValueError(f"{label} settings are missing: {', '.join(missing)}")
    invalid = sorted(key for key in required if not isinstance(values[key], str))
    if invalid:
        raise ValueError(f"{label} settings must be strings: {', '.join(invalid)}")


def _duration_seconds(value: str) -> float:
    position = 0
    seconds = 0.0
    units = {"ms": 0.001, "s": 1, "m": 60, "h": 3600}
    for match in _DURATION_PART.finditer(value.strip()):
        if match.start() != position:
            raise ValueError(f"invalid duration setting: {value!r}")
        seconds += int(match.group(1)) * units[match.group(2)]
        position = match.end()
    if position == 0 or position != len(value.strip()):
        raise ValueError(f"invalid duration setting: {value!r}")
    return seconds


def csv_list(value: str) -> list[str]:
    """Split a comma-separated setting, trimming blanks, keeping order."""
    return [part.strip() for part in value.split(",") if part.strip()]


def _threshold_seconds(name: str, value: str) -> float:
    """An idle-pause threshold: 0 disables it, otherwise at least one minute.

    The control plane silently falls back to its built-in default when a
    duration does not parse, so a typo would quietly re-enable idle teardown.
    Reject it here instead.
    """
    value = value.strip()
    if value == "0":
        return 0
    try:
        seconds = _duration_seconds(value)
    except ValueError as error:
        raise ValueError(f"{name} must be 0 or a duration such as 15m") from error
    if seconds < 60:
        raise ValueError(f"{name} must be 0 (disabled) or at least 1m")
    return seconds


def validate_offering(
    *,
    providers: list[str],
    default_provider: str,
    gated_providers: list[str],
    default_org_capabilities: list[str],
    default_org_billing: str,
    paywalled_features: list[str],
    idle_pause_threshold: str,
    freestyle_idle_pause_threshold: str,
    coder_readable_names: str,
    coder_worker_working_folder: str,
    staging_providers: list[str] | None = None,
) -> list[str]:
    """Validate a deployment's offering settings; return the served providers.

    staging_providers is set for a production promotion: production may serve
    fewer providers than staging (for example coder only while Freestyle is
    still being proven), but never one staging has not exercised.
    """
    served = resolve_sandbox_providers(default_provider, providers)
    if staging_providers is not None:
        untested = [provider for provider in served if provider not in staging_providers]
        if untested:
            raise ValueError(
                "production may only serve providers staging runs "
                f"({','.join(staging_providers)}); not exercised in staging: "
                + ",".join(untested)
            )
    unknown = [provider for provider in gated_providers if provider not in PROVIDER_SECRET_ENV]
    if unknown:
        raise ValueError(
            "AO_CLOUD_CAPABILITY_GATED_PROVIDERS names unknown providers: "
            + ",".join(unknown)
        )
    for label, tokens in (
        ("AO_CLOUD_DEFAULT_ORG_CAPABILITIES", default_org_capabilities),
        ("AO_CLOUD_PAYWALLED_FEATURES", paywalled_features),
    ):
        invalid = [token for token in tokens if not _TOKEN.fullmatch(token)]
        if invalid:
            raise ValueError(f"{label} has invalid entries: " + ",".join(invalid))
    if default_org_billing not in ORG_BILLING_MODES:
        raise ValueError(
            "AO_CLOUD_DEFAULT_ORG_BILLING must be one of " + ", ".join(ORG_BILLING_MODES)
        )
    _threshold_seconds("AO_CLOUD_IDLE_PAUSE_THRESHOLD", idle_pause_threshold)
    _threshold_seconds(
        "AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD", freestyle_idle_pause_threshold
    )
    _rollout_flag("AO_CLOUD_CODER_READABLE_NAMES", coder_readable_names)
    _rollout_flag("AO_CLOUD_CODER_WORKER_WORKING_FOLDER", coder_worker_working_folder)
    return served


def _rollout_flag(name: str, value: str) -> bool:
    """A Coder rollout flag's value: exactly true or false."""
    value = value.strip()
    if value not in ("true", "false"):
        raise ValueError(f"{name} must be true or false")
    return value == "true"


def stripe_price_ids(stripe: dict[str, Any]) -> dict[str, str]:
    """The plan-to-price map, stored as a JSON object or a JSON object string."""
    raw = stripe.get("price_ids")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw) if raw.strip() else None
        except json.JSONDecodeError as error:
            raise ValueError("Stripe price_ids must be valid JSON") from error
    if not isinstance(raw, dict) or not raw:
        raise ValueError("Stripe price_ids must map plan names to price ids")
    for plan, price in raw.items():
        if (
            not isinstance(plan, str)
            or not plan.strip()
            or not isinstance(price, str)
            or not price.strip().startswith("price_")
        ):
            raise ValueError(f"Stripe price_ids: plan {plan!r} must map to a price_ id")
    return raw


def validate_stripe_settings(stripe: dict[str, Any], *, environment: str) -> None:
    """Validate the optional ao-cloud/<environment>/stripe secret document."""
    _require_secret_strings("Stripe", stripe, STRIPE_SECRET_ENV.values())
    for key in STRIPE_SECRET_ENV.values():
        if not stripe[key].strip():
            raise ValueError(f"Stripe {key} must not be empty")
    # A live key in staging or a test key in production is a wiring mistake
    # that would either charge real cards from staging or never charge at all.
    secret_key = stripe["secret_key"].strip()
    if environment == "production" and re.match(r"^(sk|rk)_test_", secret_key):
        raise ValueError("production Stripe secret_key must be a live key")
    if environment != "production" and re.match(r"^(sk|rk)_live_", secret_key):
        raise ValueError(f"{environment} Stripe secret_key must not be a live key")
    stripe_price_ids(stripe)
    portal = stripe.get("portal_configuration", "")
    if not isinstance(portal, str) or (portal.strip() and not portal.strip().startswith("bpc_")):
        raise ValueError("Stripe portal_configuration must be empty or a bpc_ id")
    cutoff = stripe.get("exempt_orgs_created_before", "")
    if not isinstance(cutoff, str):
        raise ValueError("Stripe exempt_orgs_created_before must be an RFC 3339 string")
    if cutoff.strip():
        try:
            parsed = datetime.fromisoformat(cutoff.strip().replace("Z", "+00:00"))
        except ValueError as error:
            raise ValueError(
                "Stripe exempt_orgs_created_before must be an RFC 3339 timestamp"
            ) from error
        if parsed.tzinfo is None or "T" not in cutoff:
            raise ValueError(
                "Stripe exempt_orgs_created_before must be an RFC 3339 timestamp"
            )


def task_definition_freestyle_snapshots(source: dict[str, Any]) -> set[str]:
    """Snapshot ids a control-plane task definition boots Freestyle sessions from."""
    snapshots: set[str] = set()
    for container in source["taskDefinition"]["containerDefinitions"]:
        if container.get("name") != "control-plane":
            continue
        for item in container.get("environment", []):
            if item.get("name") != "AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS":
                continue
            raw = item.get("value", "")
            if not raw.strip():
                continue
            mapping = json.loads(raw)
            if not isinstance(mapping, dict):
                raise ValueError("AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS is not an object")
            snapshots |= {value for value in mapping.values() if isinstance(value, str)}
    return snapshots


def stale_harness_snapshots(
    listing: dict[str, Any],
    harnesses: list[str],
    protected: set[str],
    *,
    keep: int = 2,
) -> list[str]:
    """Harness snapshot ids safe to delete.

    Keeps the newest `keep` per harness (the set just baked and the one before
    it, which older sessions still name for a repair) and never returns a
    protected id: one a running or previous task definition, or the Freestyle
    secret, still references.
    """
    stale: list[str] = []
    snapshots = listing.get("snapshots") or []
    for harness in harnesses:
        pattern = re.compile(rf"^ao-{re.escape(harness)}-[0-9a-f]{{12}}-[0-9]{{14}}$")
        ours = sorted(
            (item for item in snapshots if pattern.fullmatch(item.get("slug") or "")),
            key=lambda item: item.get("createdAt") or "",
            reverse=True,
        )
        stale.extend(
            item["id"]
            for item in ours[keep:]
            if item.get("id") and item["id"] not in protected
        )
    return stale


def validate_digest_image(image: str, label: str) -> None:
    if not _DIGEST_IMAGE.fullmatch(image):
        raise ValueError(f"{label} image must be pinned to a sha256 digest")


def build_task_definition(
    source: dict[str, Any],
    *,
    family: str,
    container_name: str,
    image: str,
    release: str,
    environment: str,
    log_group: str,
    region: str,
    runtime_database_user: str = "",
    worker_image: str = "",
    sandbox_provider: str = "nodeops",
    sandbox_providers: list[str] | None = None,
    environment_overrides: dict[str, str] | None = None,
    secret_overrides: dict[str, str] | None = None,
) -> dict[str, Any]:
    task = source["taskDefinition"]
    payload = {
        key: copy.deepcopy(value)
        for key, value in task.items()
        if key in TASK_KEYS
    }
    payload.update(
        {
            "family": family,
            "taskRoleArn": task["taskRoleArn"],
            "executionRoleArn": task["executionRoleArn"],
        }
    )
    container = next(
        item
        for item in payload["containerDefinitions"]
        if item["name"] == container_name
    )
    validate_digest_image(image, container_name)
    container["image"] = image

    environment_overrides = environment_overrides or {}
    secret_overrides = secret_overrides or {}
    if sandbox_provider not in PROVIDER_SECRET_ENV:
        raise ValueError(f"unsupported hosted sandbox provider: {sandbox_provider}")
    providers = resolve_sandbox_providers(sandbox_provider, sandbox_providers)
    # Prune only the env/secret names of providers this task does NOT serve, so a
    # multi-provider task keeps every available provider's secrets (a
    # nodeops-primary deploy must not drop coder credentials, and vice versa).
    prune_names = _inactive_provider_env_names(providers) | DEPLOY_MANAGED_ENV
    auto_pause_names = {PROVIDER_AUTO_PAUSE_ENV, FREESTYLE_AUTO_PAUSE_ENV}
    if auto_pause_names & (environment_overrides.keys() | secret_overrides.keys()):
        raise ValueError("provider auto-pause must not be configured by deployment")
    values = {
        item["name"]: item["value"]
        for item in container.get("environment", [])
        if item["name"] not in auto_pause_names
        and item["name"] not in prune_names
    }
    values["AO_CLOUD_RELEASE"] = release
    if container_name == "control-plane":
        validate_digest_image(worker_image, "worker")
        values.update(
            {
                "AO_CLOUD_ENV": environment,
                "AO_CLOUD_HTTP_ADDRESS": ":8080",
                "AO_CLOUD_LOCAL_AUTH": "false",
                "AO_CLOUD_MIGRATE_ON_STARTUP": "false",
                "AO_CLOUD_SANDBOX_PROVIDER": sandbox_provider,
                "AO_CLOUD_SANDBOX_PROVIDERS": ",".join(providers),
                "AO_CLOUD_TERMINAL_STREAM": "1",
                "AO_CLOUD_TERMINAL_RELAY": "1",
                "AO_CLOUD_WORKER_BINARY_PATH": WORKER_BINARY_PATH,
                "AO_CLOUD_WORKER_HELPER_BINARY_PATH": WORKER_HELPER_BINARY_PATH,
            }
        )
    elif container_name == "migration":
        values["AO_CLOUD_RUNTIME_DATABASE_USER"] = runtime_database_user
    values.update(environment_overrides)
    # An empty override removes the name: the control plane reads an unset and
    # an empty value alike, and ECS keeps the definition tidy.
    values = {name: value for name, value in values.items() if value != ""}
    container["environment"] = [
        {"name": name, "value": value}
        for name, value in sorted(values.items())
    ]
    secrets = {
        item["name"]: item["valueFrom"]
        for item in container.get("secrets", [])
        if item["name"] not in auto_pause_names
        and item["name"] not in prune_names
    }
    secrets.update(secret_overrides)
    if container_name == "control-plane":
        required_secrets = set(WORKER_SECRET_ENV) | _required_provider_secrets(
            providers
        )
        missing = sorted(required_secrets - secrets.keys())
        if missing:
            raise ValueError(
                "control-plane task is missing hosted secrets: "
                + ", ".join(missing)
            )
        plaintext = sorted((required_secrets | set(STRIPE_SECRET_ENV) | set(SUPPORT_SECRET_ENV)) & values.keys())
        if plaintext:
            raise ValueError(
                "hosted settings must not be plaintext environment values: "
                + ", ".join(plaintext)
            )
        _validate_stripe_task(values, secrets)
        _validate_offering_task(values, providers)
    container["secrets"] = [
        {"name": name, "valueFrom": value}
        for name, value in sorted(secrets.items())
    ]
    log_options = container["logConfiguration"]["options"]
    log_options.update(
        {
            "awslogs-group": log_group,
            "awslogs-region": region,
            "awslogs-stream-prefix": (
                "api" if container_name == "control-plane" else "migration"
            ),
        }
    )
    payload["tags"] = [
        {"key": "Project", "value": "ao-cloud"},
        {"key": "Environment", "value": environment},
        {"key": "Release", "value": release},
    ]
    if container_name == "control-plane":
        payload["tags"].append({"key": "WorkerImage", "value": worker_image})
    reject_cross_environment_references(payload, environment)
    return payload


def _validate_stripe_task(values: dict[str, str], secrets: dict[str, str]) -> None:
    """Billing is all or nothing: both Stripe secrets plus the price ids."""
    stripe_secrets = set(STRIPE_SECRET_ENV) & secrets.keys()
    if stripe_secrets and stripe_secrets != set(STRIPE_SECRET_ENV):
        raise ValueError(
            "Stripe billing needs both "
            + " and ".join(sorted(STRIPE_SECRET_ENV))
        )
    if stripe_secrets and "AO_CLOUD_STRIPE_PRICE_IDS" not in values:
        raise ValueError("Stripe billing needs AO_CLOUD_STRIPE_PRICE_IDS")
    if not stripe_secrets:
        orphaned = sorted(STRIPE_PLAINTEXT_ENV & values.keys())
        if orphaned:
            raise ValueError(
                "Stripe settings without a Stripe secret: " + ", ".join(orphaned)
            )


def _validate_offering_task(values: dict[str, str], providers: list[str]) -> None:
    """Reject a malformed offering value before it reaches a running task."""
    gated = csv_list(values.get("AO_CLOUD_CAPABILITY_GATED_PROVIDERS", ""))
    unknown = [provider for provider in gated if provider not in PROVIDER_SECRET_ENV]
    if unknown:
        raise ValueError(
            "AO_CLOUD_CAPABILITY_GATED_PROVIDERS names unknown providers: "
            + ",".join(unknown)
        )
    billing = values.get("AO_CLOUD_DEFAULT_ORG_BILLING")
    if billing is not None and billing not in ORG_BILLING_MODES:
        raise ValueError(
            "AO_CLOUD_DEFAULT_ORG_BILLING must be one of " + ", ".join(ORG_BILLING_MODES)
        )
    for name in ("AO_CLOUD_IDLE_PAUSE_THRESHOLD", "AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD"):
        if name in values:
            _threshold_seconds(name, values[name])
    for name in CODER_ROLLOUT_FLAGS:
        if name in values:
            _rollout_flag(name, values[name])


def validate_task_artifacts(
    source: dict[str, Any],
    *,
    container_name: str,
    control_image: str,
    worker_image: str,
) -> None:
    validate_digest_image(control_image, "control-plane")
    validate_digest_image(worker_image, "worker")
    task = source["taskDefinition"]
    container = next(
        item
        for item in task["containerDefinitions"]
        if item["name"] == container_name
    )
    if container.get("image") != control_image:
        raise ValueError("task definition uses an unexpected control-plane image")
    environment = {
        item["name"]: item["value"] for item in container.get("environment", [])
    }
    if environment.get("AO_CLOUD_WORKER_BINARY_PATH") != WORKER_BINARY_PATH:
        raise ValueError("task definition does not use packaged /ao-worker")
    if (
        environment.get("AO_CLOUD_WORKER_HELPER_BINARY_PATH")
        != WORKER_HELPER_BINARY_PATH
    ):
        raise ValueError("task definition does not use packaged /ao helper")
    if PROVIDER_AUTO_PAUSE_ENV in environment or FREESTYLE_AUTO_PAUSE_ENV in environment:
        raise ValueError("task definition configures provider auto-pause")
    if environment.get("AO_CLOUD_TERMINAL_STREAM") != "1":
        raise ValueError("task definition does not enable terminal streaming")
    if environment.get("AO_CLOUD_TERMINAL_RELAY") != "1":
        raise ValueError("task definition does not enable terminal relay")
    sandbox_provider = environment.get("AO_CLOUD_SANDBOX_PROVIDER", "")
    if sandbox_provider not in PROVIDER_SECRET_ENV:
        raise ValueError("task definition uses an unsupported sandbox provider")
    # The task may serve more than one provider; require every available
    # provider's secrets and reject only providers it does not serve.
    providers_env = environment.get("AO_CLOUD_SANDBOX_PROVIDERS", "")
    providers = [
        provider.strip()
        for provider in providers_env.split(",")
        if provider.strip()
    ] or [sandbox_provider]
    for provider in providers:
        if provider not in PROVIDER_SECRET_ENV:
            raise ValueError("task definition uses an unsupported sandbox provider")
    if sandbox_provider not in providers:
        raise ValueError(
            "task definition primary provider is not in its available providers"
        )
    secrets = {
        item["name"]: item["valueFrom"] for item in container.get("secrets", [])
    }
    required_secrets = set(WORKER_SECRET_ENV) | _required_provider_secrets(providers)
    missing = sorted(required_secrets - secrets.keys())
    if missing:
        raise ValueError(
            "task definition is missing hosted secrets: " + ", ".join(missing)
        )
    other_provider_secrets = set().union(
        *(
            set(fields)
            for provider, fields in PROVIDER_SECRET_ENV.items()
            if provider not in providers
        ),
        set(),
    )
    retained = sorted(other_provider_secrets & secrets.keys())
    if retained:
        raise ValueError(
            "task definition retains inactive provider secrets: "
            + ", ".join(retained)
        )
    if PROVIDER_AUTO_PAUSE_ENV in secrets:
        raise ValueError("task definition loads provider auto-pause from a secret")
    tags = {item["key"]: item["value"] for item in source.get("tags", [])}
    if tags.get("WorkerImage") != worker_image:
        raise ValueError("task definition uses an unexpected worker image")


def reject_cross_environment_references(
    payload: dict[str, Any], environment: str
) -> None:
    forbidden_environment = (
        "staging" if environment == "production" else "production"
    )
    stack = [payload]
    while stack:
        value = stack.pop()
        if isinstance(value, dict):
            stack.extend(value.values())
        elif isinstance(value, list):
            stack.extend(value)
        elif isinstance(value, str):
            lowered = value.lower()
            if (
                f"/{forbidden_environment}/" in lowered
                or f"ao-cloud-{forbidden_environment}" in lowered
                or f"{forbidden_environment}-api." in lowered
                or f"/ao-cloud/{forbidden_environment}/" in lowered
            ):
                raise ValueError(
                    f"{environment} task contains {forbidden_environment} "
                    f"reference: {value}"
                )


def validate_service(
    *,
    service: dict[str, Any],
    tasks: list[dict[str, Any]],
    targets: list[dict[str, Any]],
    alarm_state: str,
    expected_task_definition: str | None = None,
    ignore_draining: bool = False,
) -> None:
    # A rolling (200/100) deployment leaves the replaced task's target draining
    # for the target group's deregistration delay; it takes no new requests.
    if ignore_draining:
        targets = [
            target
            for target in targets
            if target.get("TargetHealth", {}).get("State") != "draining"
        ]
    desired = service.get("desiredCount", 0)
    if desired != 1:
        raise ValueError(f"desired task count is {desired}, expected exactly 1")
    if service.get("pendingCount") != 0:
        raise ValueError("service has pending tasks")
    if service.get("runningCount") != desired:
        raise ValueError("running task count does not match desired count")

    primary = [
        deployment
        for deployment in service.get("deployments", [])
        if deployment.get("status") == "PRIMARY"
    ]
    if len(primary) != 1 or primary[0].get("rolloutState") != "COMPLETED":
        raise ValueError("primary deployment is not complete")
    task_definition = expected_task_definition or service.get("taskDefinition")
    if primary[0].get("taskDefinition") != task_definition:
        raise ValueError("primary deployment uses an unexpected task definition")
    if len(tasks) != desired:
        raise ValueError("running task inventory does not match desired count")
    if any(task.get("taskDefinitionArn") != task_definition for task in tasks):
        raise ValueError("running tasks contain a mixed task-definition revision")
    if len(targets) != desired:
        raise ValueError("registered ALB target count does not match running tasks")
    if any(
        target.get("TargetHealth", {}).get("State") != "healthy"
        for target in targets
    ):
        raise ValueError("one or more ALB targets are unhealthy")
    if alarm_state != "OK":
        raise ValueError(f"deployment alarm state is {alarm_state}, expected OK")
