#!/usr/bin/env python3
"""Pure helpers for producing and validating AO Cloud ECS deployments."""

from __future__ import annotations

import copy
import json
import posixpath
import re
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
    "coder": CODER_SECRET_ENV,
    "freestyle": FREESTYLE_SECRET_ENV,
}
FREESTYLE_PLAINTEXT_ENV = {"AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS"}
PROVIDER_ENV_NAMES = (
    set(CODER_SECRET_ENV)
    | set(FREESTYLE_SECRET_ENV)
    | FREESTYLE_PLAINTEXT_ENV
)
# Settings of the retired NodeOps provider. A task definition rendered from an
# older revision still carries them, so they are always pruned; that is what
# lets the retired ao-cloud/<env>/nodeops secret be deleted.
RETIRED_PROVIDER_ENV_PREFIX = "AO_CLOUD_NODEOPS_"
# Freestyle's own idle pause is not reported as an idle stop, so the reconciler
# would resume it at once; the control plane's idle scanner owns pausing.
FREESTYLE_AUTO_PAUSE_ENV = "AO_CLOUD_FREESTYLE_AUTO_PAUSE_SECONDS"
WORKER_BINARY_PATH = "/ao-worker"
WORKER_HELPER_BINARY_PATH = "/ao"
_DIGEST_IMAGE = re.compile(r"^.+@sha256:[0-9a-f]{64}$")
_DURATION_PART = re.compile(r"(\d+)(ms|s|m|h)")


def resolve_sandbox_providers(
    sandbox_provider: str, sandbox_providers: list[str] | None
) -> list[str]:
    """The full set of sandbox providers a control-plane task serves.

    A single-provider deployment passes sandbox_providers=None and gets exactly
    [sandbox_provider], preserving the historical behavior. A multi-provider
    deployment (for example coder,freestyle) passes every provider it offers so
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
    retains, for example, its coder secrets during a freestyle-primary deploy.
    """
    keep: set[str] = set()
    for provider in providers:
        keep |= set(PROVIDER_SECRET_ENV[provider])
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
    provider: str = "coder",
) -> None:
    if provider not in PROVIDER_SECRET_ENV:
        raise ValueError(f"unsupported hosted sandbox provider: {provider}")
    if provider == "freestyle":
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
    sandbox_provider: str = "coder",
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
    # freestyle-primary deploy must not drop coder credentials, and vice versa).
    prune_names = _inactive_provider_env_names(providers)
    auto_pause_names = {FREESTYLE_AUTO_PAUSE_ENV}
    overridden = environment_overrides.keys() | secret_overrides.keys()
    if auto_pause_names & overridden:
        raise ValueError("provider auto-pause must not be configured by deployment")
    if any(name.startswith(RETIRED_PROVIDER_ENV_PREFIX) for name in overridden):
        raise ValueError("retired provider settings must not be configured by deployment")
    values = {
        item["name"]: item["value"]
        for item in container.get("environment", [])
        if item["name"] not in auto_pause_names
        and item["name"] not in prune_names
        and not item["name"].startswith(RETIRED_PROVIDER_ENV_PREFIX)
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
    container["environment"] = [
        {"name": name, "value": value}
        for name, value in sorted(values.items())
    ]
    secrets = {
        item["name"]: item["valueFrom"]
        for item in container.get("secrets", [])
        if item["name"] not in auto_pause_names
        and item["name"] not in prune_names
        and not item["name"].startswith(RETIRED_PROVIDER_ENV_PREFIX)
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
        plaintext = sorted(required_secrets & values.keys())
        if plaintext:
            raise ValueError(
                "hosted settings must not be plaintext environment values: "
                + ", ".join(plaintext)
            )
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
    if FREESTYLE_AUTO_PAUSE_ENV in environment:
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
    if FREESTYLE_AUTO_PAUSE_ENV in secrets:
        raise ValueError("task definition loads provider auto-pause from a secret")
    if any(
        name.startswith(RETIRED_PROVIDER_ENV_PREFIX)
        for name in environment.keys() | secrets.keys()
    ):
        raise ValueError("task definition retains retired provider settings")
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
) -> None:
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
