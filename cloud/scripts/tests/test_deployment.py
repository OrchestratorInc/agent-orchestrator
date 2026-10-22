import copy
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))

from lib.deployment import (
    CODER_SECRET_ENV,
    FREESTYLE_SECRET_ENV,
    NODEOPS_SECRET_ENV,
    STRIPE_SECRET_ENV,
    WORKER_SECRET_ENV,
    build_task_definition,
    resolve_sandbox_providers,
    secret_environment,
    stale_harness_snapshots,
    task_definition_freestyle_snapshots,
    validate_hosted_settings,
    validate_offering,
    validate_service,
    validate_stripe_settings,
    validate_task_artifacts,
)


DIGEST = "a" * 64
CONTROL_IMAGE = f"registry/ao-cloud-control-plane@sha256:{DIGEST}"
WORKER_IMAGE = f"registry/ao-cloud-worker@sha256:{'b' * 64}"


def hosted_secret_overrides(environment="production"):
    return secret_environment(
        f"arn:secret:ao-cloud/{environment}/nodeops", NODEOPS_SECRET_ENV
    ) | secret_environment(
        f"arn:secret:ao-cloud/{environment}/worker", WORKER_SECRET_ENV
    )


def coder_secret_overrides(environment="production"):
    return secret_environment(
        f"arn:secret:ao-cloud/{environment}/coder", CODER_SECRET_ENV
    ) | secret_environment(
        f"arn:secret:ao-cloud/{environment}/worker", WORKER_SECRET_ENV
    )


def multi_provider_secret_overrides(environment="staging"):
    return (
        secret_environment(
            f"arn:secret:ao-cloud/{environment}/nodeops", NODEOPS_SECRET_ENV
        )
        | secret_environment(
            f"arn:secret:ao-cloud/{environment}/coder", CODER_SECRET_ENV
        )
        | secret_environment(
            f"arn:secret:ao-cloud/{environment}/worker", WORKER_SECRET_ENV
        )
    )


def hosted_settings():
    return (
        {
            "base_url": "https://api.sb.createos.sh",
            "api_key": "nodeops-secret",
            "default_shape": "s-4vcpu-8gb",
            "default_rootfs": "devbox:1",
            "ingress": "disabled",
            "ssh_key_path": "",
            "region": "eu-north-1",
            "worker_token_ttl": "15m",
        },
        {
            "signing_key": "w" * 64,
            "max_active_sandboxes_per_org": "10",
            "sandbox_reconcile_interval": "2s",
            "sandbox_startup_timeout": "3m",
            "worker_heartbeat_timeout": "1m",
        },
    )


def coder_settings():
    return (
        {
            "url": "https://coder.example.com",
            "token": "coder-secret",
            "owner": "ao-integration",
            "template_id": "template-uuid",
            "agent_name": "main",
            "parameters_json": '{"instance_type":"t3.medium"}',
            "durable_root": "/home/coder",
            "worker_token_ttl": "15m",
        },
        hosted_settings()[1],
    )


def freestyle_secret_overrides(environment="staging"):
    return secret_environment(
        f"arn:secret:ao-cloud/{environment}/freestyle", FREESTYLE_SECRET_ENV
    ) | secret_environment(
        f"arn:secret:ao-cloud/{environment}/worker", WORKER_SECRET_ENV
    )


def freestyle_settings():
    return (
        {
            "api_key": "freestyle-secret",
            "default_snapshot": "snap-claude",
            "worker_token_ttl": "15m",
            "snapshot_by_harness": (
                '{"claude-code":"snap-claude","codex":"snap-codex","cursor":"snap-cursor"}'
            ),
        },
        hosted_settings()[1],
    )


def task_source(environment="production"):
    return {
        "taskDefinition": {
            "taskRoleArn": f"arn:task:{environment}",
            "executionRoleArn": f"arn:execution:{environment}",
            "networkMode": "awsvpc",
            "requiresCompatibilities": ["FARGATE"],
            "cpu": "256",
            "memory": "512",
            "containerDefinitions": [
                {
                    "name": "control-plane",
                    "image": "old",
                    "environment": [
                        {"name": "AO_CLOUD_ENV", "value": environment},
                        {"name": "AO_CLOUD_PUBLIC_URL", "value": "https://api.aoagents.dev"},
                    ],
                    "secrets": [
                        {
                            "name": "AO_CLOUD_DATABASE_URL",
                            "valueFrom": f"arn:secret:ao-cloud/{environment}/database-url",
                        }
                    ],
                    "logConfiguration": {
                        "options": {
                            "awslogs-group": f"/ao-cloud/{environment}/control-plane"
                        }
                    },
                }
            ],
        }
    }


def healthy_service():
    task = "arn:task-definition:production:7"
    return (
        {
            "desiredCount": 1,
            "runningCount": 1,
            "pendingCount": 0,
            "taskDefinition": task,
            "deployments": [
                {
                    "status": "PRIMARY",
                    "rolloutState": "COMPLETED",
                    "taskDefinition": task,
                }
            ],
        },
        [{"taskDefinitionArn": task}],
        [{"TargetHealth": {"State": "healthy"}}],
    )


class TaskDefinitionTests(unittest.TestCase):
    def test_preserves_production_secrets_and_updates_release(self):
        payload = build_task_definition(
            task_source(),
            family="ao-cloud-production-api",
            container_name="control-plane",
            image=CONTROL_IMAGE,
            worker_image=WORKER_IMAGE,
            release="abc123",
            environment="production",
            log_group="/ao-cloud/production/control-plane",
            region="eu-north-1",
            environment_overrides={
                "AO_CLOUD_PUBLIC_URL": "https://api.aoagents.dev"
            },
            secret_overrides={
                "AO_CLOUD_GITHUB_APP_ID": (
                    "arn:secret:ao-cloud/production/github:app_id::"
                )
            }
            | hosted_secret_overrides(),
        )
        container = payload["containerDefinitions"][0]
        self.assertEqual(container["image"], CONTROL_IMAGE)
        self.assertEqual(
            container["secrets"][0]["valueFrom"],
            "arn:secret:ao-cloud/production/database-url",
        )
        environment = {
            item["name"]: item["value"] for item in container["environment"]
        }
        self.assertEqual(environment["AO_CLOUD_RELEASE"], "abc123")
        self.assertEqual(environment["AO_CLOUD_ENV"], "production")
        self.assertEqual(environment["AO_CLOUD_WORKER_BINARY_PATH"], "/ao-worker")
        self.assertEqual(environment["AO_CLOUD_WORKER_HELPER_BINARY_PATH"], "/ao")
        self.assertEqual(environment["AO_CLOUD_TERMINAL_STREAM"], "1")
        self.assertEqual(environment["AO_CLOUD_TERMINAL_RELAY"], "1")
        secrets = {
            item["name"]: item["valueFrom"] for item in container["secrets"]
        }
        self.assertEqual(
            secrets["AO_CLOUD_GITHUB_APP_ID"],
            "arn:secret:ao-cloud/production/github:app_id::",
        )
        self.assertNotIn("AO_CLOUD_NODEOPS_AUTO_PAUSE_MINUTES", environment)
        self.assertNotIn("AO_CLOUD_NODEOPS_AUTO_PAUSE_MINUTES", secrets)
        tags = {item["key"]: item["value"] for item in payload["tags"]}
        self.assertEqual(tags["WorkerImage"], WORKER_IMAGE)

    def test_rejects_staging_reference_in_production_template(self):
        source = task_source()
        source["taskDefinition"]["containerDefinitions"][0]["secrets"][0][
            "valueFrom"
        ] = "arn:secret:ao-cloud/staging/database-url"
        with self.assertRaisesRegex(ValueError, "staging reference"):
            build_task_definition(
                source,
                family="ao-cloud-production-api",
                container_name="control-plane",
                image=CONTROL_IMAGE,
                worker_image=WORKER_IMAGE,
                release="abc123",
                environment="production",
                log_group="/ao-cloud/production/control-plane",
                region="eu-north-1",
                secret_overrides=hosted_secret_overrides(),
            )

    def test_rejects_production_reference_in_staging_template(self):
        source = task_source("staging")
        source["taskDefinition"]["containerDefinitions"][0]["secrets"][0][
            "valueFrom"
        ] = "arn:secret:ao-cloud/production/database-url"
        with self.assertRaisesRegex(ValueError, "production reference"):
            build_task_definition(
                source,
                family="ao-cloud-staging-api",
                container_name="control-plane",
                image=CONTROL_IMAGE,
                worker_image=WORKER_IMAGE,
                release="abc123",
                environment="staging",
                log_group="/ao-cloud/staging/control-plane",
                region="eu-north-1",
                secret_overrides=hosted_secret_overrides("staging"),
            )

    def test_rejects_provider_auto_pause_override(self):
        with self.assertRaisesRegex(ValueError, "auto-pause"):
            build_task_definition(
                task_source(),
                family="ao-cloud-production-api",
                container_name="control-plane",
                image=CONTROL_IMAGE,
                worker_image=WORKER_IMAGE,
                release="abc123",
                environment="production",
                log_group="/ao-cloud/production/control-plane",
                region="eu-north-1",
                secret_overrides=hosted_secret_overrides()
                | {"AO_CLOUD_NODEOPS_AUTO_PAUSE_MINUTES": "arn:secret:field::"},
            )

    def test_validates_rendered_artifact_contract(self):
        payload = build_task_definition(
            task_source(),
            family="ao-cloud-production-api",
            container_name="control-plane",
            image=CONTROL_IMAGE,
            worker_image=WORKER_IMAGE,
            release="abc123",
            environment="production",
            log_group="/ao-cloud/production/control-plane",
            region="eu-north-1",
            secret_overrides=hosted_secret_overrides(),
        )
        validate_task_artifacts(
            {"taskDefinition": payload, "tags": payload["tags"]},
            container_name="control-plane",
            control_image=CONTROL_IMAGE,
            worker_image=WORKER_IMAGE,
        )

    def test_switches_from_nodeops_to_coder_without_retaining_credentials(self):
        source = task_source("staging")
        container = source["taskDefinition"]["containerDefinitions"][0]
        container["environment"].append(
            {
                "name": "AO_CLOUD_NODEOPS_ROOTFS_BY_HARNESS",
                "value": '{"claude-code":"template"}',
            }
        )
        container["secrets"].extend(
            {"name": name, "valueFrom": value}
            for name, value in hosted_secret_overrides("staging").items()
        )
        payload = build_task_definition(
            source,
            family="ao-cloud-staging-api",
            container_name="control-plane",
            image=CONTROL_IMAGE,
            worker_image=WORKER_IMAGE,
            release="abc123",
            environment="staging",
            log_group="/ao-cloud/staging/control-plane",
            region="eu-north-1",
            sandbox_provider="coder",
            secret_overrides=coder_secret_overrides("staging"),
        )
        rendered = payload["containerDefinitions"][0]
        environment = {item["name"]: item["value"] for item in rendered["environment"]}
        secrets = {item["name"]: item["valueFrom"] for item in rendered["secrets"]}
        self.assertEqual(environment["AO_CLOUD_SANDBOX_PROVIDER"], "coder")
        self.assertFalse(set(NODEOPS_SECRET_ENV) & secrets.keys())
        self.assertNotIn("AO_CLOUD_NODEOPS_ROOTFS_BY_HARNESS", environment)
        self.assertTrue(set(CODER_SECRET_ENV) <= secrets.keys())
        validate_task_artifacts(
            {"taskDefinition": payload, "tags": payload["tags"]},
            container_name="control-plane",
            control_image=CONTROL_IMAGE,
            worker_image=WORKER_IMAGE,
        )

    def test_multi_provider_keeps_both_provider_secrets(self):
        source = task_source("staging")
        container = source["taskDefinition"]["containerDefinitions"][0]
        # A source that already carries both providers' secrets (the shape of a
        # live multi-provider task) must keep them, not prune one.
        container["environment"].append(
            {
                "name": "AO_CLOUD_NODEOPS_ROOTFS_BY_HARNESS",
                "value": '{"claude-code":"template"}',
            }
        )
        container["secrets"].extend(
            {"name": name, "valueFrom": value}
            for name, value in multi_provider_secret_overrides("staging").items()
        )
        payload = build_task_definition(
            source,
            family="ao-cloud-staging-api",
            container_name="control-plane",
            image=CONTROL_IMAGE,
            worker_image=WORKER_IMAGE,
            release="abc123",
            environment="staging",
            log_group="/ao-cloud/staging/control-plane",
            region="eu-north-1",
            sandbox_provider="nodeops",
            sandbox_providers=["nodeops", "coder"],
            secret_overrides=multi_provider_secret_overrides("staging"),
        )
        rendered = payload["containerDefinitions"][0]
        environment = {item["name"]: item["value"] for item in rendered["environment"]}
        secrets = {item["name"]: item["valueFrom"] for item in rendered["secrets"]}
        self.assertEqual(environment["AO_CLOUD_SANDBOX_PROVIDER"], "nodeops")
        self.assertEqual(environment["AO_CLOUD_SANDBOX_PROVIDERS"], "nodeops,coder")
        self.assertEqual(
            environment["AO_CLOUD_NODEOPS_ROOTFS_BY_HARNESS"],
            '{"claude-code":"template"}',
        )
        self.assertTrue(set(NODEOPS_SECRET_ENV) <= secrets.keys())
        self.assertTrue(set(CODER_SECRET_ENV) <= secrets.keys())
        validate_task_artifacts(
            {"taskDefinition": payload, "tags": payload["tags"]},
            container_name="control-plane",
            control_image=CONTROL_IMAGE,
            worker_image=WORKER_IMAGE,
        )

    def test_multi_provider_requires_every_provider_secret(self):
        with self.assertRaisesRegex(ValueError, "missing hosted secrets"):
            build_task_definition(
                task_source("staging"),
                family="ao-cloud-staging-api",
                container_name="control-plane",
                image=CONTROL_IMAGE,
                worker_image=WORKER_IMAGE,
                release="abc123",
                environment="staging",
                log_group="/ao-cloud/staging/control-plane",
                region="eu-north-1",
                sandbox_provider="nodeops",
                sandbox_providers=["nodeops", "coder"],
                secret_overrides=hosted_secret_overrides("staging"),
            )

    def test_rejects_primary_provider_outside_available_set(self):
        with self.assertRaisesRegex(ValueError, "must be one of the available providers"):
            build_task_definition(
                task_source("staging"),
                family="ao-cloud-staging-api",
                container_name="control-plane",
                image=CONTROL_IMAGE,
                worker_image=WORKER_IMAGE,
                release="abc123",
                environment="staging",
                log_group="/ao-cloud/staging/control-plane",
                region="eu-north-1",
                sandbox_provider="nodeops",
                sandbox_providers=["coder"],
                secret_overrides=coder_secret_overrides("staging"),
            )

    def test_validate_rejects_secret_outside_available_providers(self):
        payload = build_task_definition(
            task_source("staging"),
            family="ao-cloud-staging-api",
            container_name="control-plane",
            image=CONTROL_IMAGE,
            worker_image=WORKER_IMAGE,
            release="abc123",
            environment="staging",
            log_group="/ao-cloud/staging/control-plane",
            region="eu-north-1",
            sandbox_provider="nodeops",
            sandbox_providers=["nodeops"],
            secret_overrides=hosted_secret_overrides("staging"),
        )
        rendered = payload["containerDefinitions"][0]
        # A coder secret leaks in although the available set is nodeops-only.
        rendered["secrets"].append(
            {
                "name": "AO_CLOUD_CODER_URL",
                "valueFrom": "arn:secret:ao-cloud/staging/coder:url::",
            }
        )
        with self.assertRaisesRegex(
            ValueError, "retains inactive provider secrets"
        ):
            validate_task_artifacts(
                {"taskDefinition": payload, "tags": payload["tags"]},
                container_name="control-plane",
                control_image=CONTROL_IMAGE,
                worker_image=WORKER_IMAGE,
            )

    def test_validate_backward_compat_without_providers_env(self):
        # An older task-def rendered before AO_CLOUD_SANDBOX_PROVIDERS existed
        # carries only AO_CLOUD_SANDBOX_PROVIDER. validate_task_artifacts must
        # fall back to that single provider and still accept the task (the
        # first-promote-after-upgrade case).
        payload = build_task_definition(
            task_source("staging"),
            family="ao-cloud-staging-api",
            container_name="control-plane",
            image=CONTROL_IMAGE,
            worker_image=WORKER_IMAGE,
            release="abc123",
            environment="staging",
            log_group="/ao-cloud/staging/control-plane",
            region="eu-north-1",
            sandbox_provider="nodeops",
            sandbox_providers=["nodeops"],
            secret_overrides=hosted_secret_overrides("staging"),
        )
        rendered = payload["containerDefinitions"][0]
        rendered["environment"] = [
            item
            for item in rendered["environment"]
            if item["name"] != "AO_CLOUD_SANDBOX_PROVIDERS"
        ]
        # Must not raise: the fallback derives [AO_CLOUD_SANDBOX_PROVIDER].
        validate_task_artifacts(
            {"taskDefinition": payload, "tags": payload["tags"]},
            container_name="control-plane",
            control_image=CONTROL_IMAGE,
            worker_image=WORKER_IMAGE,
        )

    def test_resolve_sandbox_providers_dedups_and_defaults(self):
        self.assertEqual(resolve_sandbox_providers("nodeops", None), ["nodeops"])
        self.assertEqual(
            resolve_sandbox_providers("nodeops", ["nodeops", "coder", "nodeops"]),
            ["nodeops", "coder"],
        )
        with self.assertRaises(ValueError):
            resolve_sandbox_providers("nodeops", ["coder"])
        with self.assertRaises(ValueError):
            resolve_sandbox_providers("nodeops", ["nodeops", "bogus"])


class FreestyleTaskDefinitionTests(unittest.TestCase):
    def test_renders_freestyle_secrets_and_snapshot_map(self):
        payload = build_task_definition(
            task_source("staging"),
            family="ao-cloud-staging-api",
            container_name="control-plane",
            image=CONTROL_IMAGE,
            release="rel",
            environment="staging",
            log_group="/ao-cloud/staging/control-plane",
            region="eu-north-1",
            worker_image=WORKER_IMAGE,
            sandbox_provider="freestyle",
            sandbox_providers=["nodeops", "freestyle"],
            environment_overrides={
                "AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS": '{"codex":"snap-codex"}'
            },
            secret_overrides=multi_provider_secret_overrides("staging")
            | freestyle_secret_overrides("staging"),
        )
        container = payload["containerDefinitions"][0]
        environment = {item["name"]: item["value"] for item in container["environment"]}
        secrets = {item["name"] for item in container["secrets"]}
        self.assertEqual(environment["AO_CLOUD_SANDBOX_PROVIDER"], "freestyle")
        self.assertEqual(environment["AO_CLOUD_SANDBOX_PROVIDERS"], "nodeops,freestyle")
        self.assertIn("AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS", environment)
        self.assertTrue(set(FREESTYLE_SECRET_ENV) <= secrets)
        self.assertTrue(set(NODEOPS_SECRET_ENV) <= secrets)

    def test_prunes_freestyle_settings_when_not_served(self):
        source = task_source("staging")
        container = source["taskDefinition"]["containerDefinitions"][0]
        container["environment"].append(
            {"name": "AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS", "value": "{}"}
        )
        container["secrets"].append(
            {
                "name": "AO_CLOUD_FREESTYLE_API_KEY",
                "valueFrom": "arn:secret:ao-cloud/staging/freestyle:api_key::",
            }
        )
        payload = build_task_definition(
            source,
            family="ao-cloud-staging-api",
            container_name="control-plane",
            image=CONTROL_IMAGE,
            release="rel",
            environment="staging",
            log_group="/ao-cloud/staging/control-plane",
            region="eu-north-1",
            worker_image=WORKER_IMAGE,
            secret_overrides=hosted_secret_overrides("staging"),
        )
        rendered = payload["containerDefinitions"][0]
        names = {item["name"] for item in rendered["environment"]} | {
            item["name"] for item in rendered["secrets"]
        }
        self.assertNotIn("AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS", names)
        self.assertNotIn("AO_CLOUD_FREESTYLE_API_KEY", names)

    def test_rejects_freestyle_auto_pause_override(self):
        with self.assertRaisesRegex(ValueError, "auto-pause"):
            build_task_definition(
                task_source("staging"),
                family="ao-cloud-staging-api",
                container_name="control-plane",
                image=CONTROL_IMAGE,
                release="rel",
                environment="staging",
                log_group="/ao-cloud/staging/control-plane",
                region="eu-north-1",
                worker_image=WORKER_IMAGE,
                sandbox_provider="freestyle",
                environment_overrides={"AO_CLOUD_FREESTYLE_AUTO_PAUSE_SECONDS": "300"},
                secret_overrides=freestyle_secret_overrides("staging"),
            )


class HostedSettingsTests(unittest.TestCase):
    def test_accepts_freestyle_settings(self):
        freestyle, worker = freestyle_settings()
        validate_hosted_settings(freestyle, worker, provider="freestyle")

    def test_rejects_freestyle_without_every_harness_snapshot(self):
        freestyle, worker = freestyle_settings()
        freestyle["snapshot_by_harness"] = '{"claude-code":"snap-claude"}'
        with self.assertRaisesRegex(ValueError, "codex, cursor"):
            validate_hosted_settings(freestyle, worker, provider="freestyle")

    def test_rejects_freestyle_auto_pause_setting(self):
        freestyle, worker = freestyle_settings()
        freestyle["auto_pause_seconds"] = "300"
        with self.assertRaisesRegex(ValueError, "auto-pause"):
            validate_hosted_settings(freestyle, worker, provider="freestyle")

    def test_accepts_complete_environment_scoped_settings(self):
        nodeops, worker = hosted_settings()
        validate_hosted_settings(nodeops, worker)

    def test_rejects_missing_worker_setting(self):
        nodeops, worker = hosted_settings()
        del worker["worker_heartbeat_timeout"]
        with self.assertRaisesRegex(ValueError, "worker_heartbeat_timeout"):
            validate_hosted_settings(nodeops, worker)

    def test_rejects_provider_auto_pause_setting(self):
        nodeops, worker = hosted_settings()
        nodeops["auto_pause_minutes"] = "30"
        with self.assertRaisesRegex(ValueError, "auto-pause"):
            validate_hosted_settings(nodeops, worker)

    def test_rejects_invalid_startup_timeout(self):
        nodeops, worker = hosted_settings()
        worker["sandbox_startup_timeout"] = "10s"
        with self.assertRaisesRegex(ValueError, "at least 30s"):
            validate_hosted_settings(nodeops, worker)

    def test_accepts_coder_settings(self):
        coder, worker = coder_settings()
        validate_hosted_settings(coder, worker, provider="coder")

    def test_rejects_invalid_coder_parameters(self):
        coder, worker = coder_settings()
        coder["parameters_json"] = '{"count":2}'
        with self.assertRaisesRegex(ValueError, "object of string values"):
            validate_hosted_settings(coder, worker, provider="coder")

    def test_rejects_non_https_coder_url(self):
        coder, worker = coder_settings()
        coder["url"] = "http://coder.example.com"
        with self.assertRaisesRegex(ValueError, "HTTPS origin"):
            validate_hosted_settings(coder, worker, provider="coder")

    def test_rejects_unsafe_coder_durable_root(self):
        coder, worker = coder_settings()
        coder["durable_root"] = "/home/coder/../ephemeral"
        with self.assertRaisesRegex(ValueError, "safe absolute non-root path"):
            validate_hosted_settings(coder, worker, provider="coder")


class ServiceValidationTests(unittest.TestCase):
    def test_accepts_stable_healthy_service(self):
        service, tasks, targets = healthy_service()
        validate_service(
            service=service,
            tasks=tasks,
            targets=targets,
            alarm_state="OK",
        )

    def test_rejects_empty_target_group(self):
        service, tasks, _ = healthy_service()
        with self.assertRaisesRegex(ValueError, "target count"):
            validate_service(
                service=service,
                tasks=tasks,
                targets=[],
                alarm_state="OK",
            )

    def test_rejects_more_than_one_replica(self):
        service, tasks, targets = healthy_service()
        service["desiredCount"] = 2
        service["runningCount"] = 2
        tasks.append(copy.deepcopy(tasks[0]))
        targets.append(copy.deepcopy(targets[0]))
        with self.assertRaisesRegex(ValueError, "expected exactly 1"):
            validate_service(
                service=service,
                tasks=tasks,
                targets=targets,
                alarm_state="OK",
            )

    def test_rejects_unexpected_task_revision(self):
        service, tasks, targets = healthy_service()
        changed = copy.deepcopy(tasks)
        changed[0]["taskDefinitionArn"] = "arn:task-definition:production:6"
        with self.assertRaisesRegex(ValueError, "mixed"):
            validate_service(
                service=service,
                tasks=changed,
                targets=targets,
                alarm_state="OK",
            )

    def test_rejects_alarm_or_incomplete_rollout(self):
        service, tasks, targets = healthy_service()
        with self.assertRaisesRegex(ValueError, "alarm state"):
            validate_service(
                service=service,
                tasks=tasks,
                targets=targets,
                alarm_state="ALARM",
            )
        service["deployments"][0]["rolloutState"] = "IN_PROGRESS"
        with self.assertRaisesRegex(ValueError, "not complete"):
            validate_service(
                service=service,
                tasks=tasks,
                targets=targets,
                alarm_state="OK",
            )

    def test_ignores_draining_targets_only_when_asked(self):
        service, tasks, targets = healthy_service()
        targets.append({"TargetHealth": {"State": "draining"}})
        with self.assertRaisesRegex(ValueError, "target count"):
            validate_service(
                service=service,
                tasks=tasks,
                targets=targets,
                alarm_state="OK",
            )
        validate_service(
            service=service,
            tasks=tasks,
            targets=targets,
            alarm_state="OK",
            ignore_draining=True,
        )

    def test_draining_does_not_hide_an_unhealthy_target(self):
        service, tasks, _ = healthy_service()
        targets = [
            {"TargetHealth": {"State": "unhealthy"}},
            {"TargetHealth": {"State": "draining"}},
        ]
        with self.assertRaisesRegex(ValueError, "unhealthy"):
            validate_service(
                service=service,
                tasks=tasks,
                targets=targets,
                alarm_state="OK",
                ignore_draining=True,
            )


def offering(**overrides):
    values = {
        "providers": ["coder", "freestyle"],
        "default_provider": "coder",
        "gated_providers": ["coder", "freestyle"],
        "default_org_capabilities": ["freestyle"],
        "default_org_billing": "self_serve",
        "paywalled_features": [],
        "idle_pause_threshold": "0",
        "freestyle_idle_pause_threshold": "15m",
        "coder_readable_names": "true",
        "coder_worker_working_folder": "true",
    }
    values.update(overrides)
    return values


class OfferingTests(unittest.TestCase):
    def test_accepts_staging_defaults(self):
        self.assertEqual(validate_offering(**offering()), ["coder", "freestyle"])

    def test_production_may_serve_a_subset_of_staging(self):
        served = validate_offering(
            **offering(providers=["coder"]),
            staging_providers=["coder", "freestyle"],
        )
        self.assertEqual(served, ["coder"])

    def test_production_rejects_a_provider_staging_does_not_run(self):
        with self.assertRaisesRegex(ValueError, "not exercised in staging: freestyle"):
            validate_offering(**offering(), staging_providers=["coder"])

    def test_production_rejects_nodeops_once_staging_dropped_it(self):
        with self.assertRaisesRegex(ValueError, "nodeops"):
            validate_offering(
                **offering(providers=["nodeops", "coder"]),
                staging_providers=["coder", "freestyle"],
            )

    def test_rejects_default_outside_providers(self):
        with self.assertRaisesRegex(ValueError, "must be one of"):
            validate_offering(**offering(providers=["coder"], default_provider="freestyle"))

    def test_rejects_unknown_gated_provider(self):
        with self.assertRaisesRegex(ValueError, "unknown providers: daytona"):
            validate_offering(**offering(gated_providers=["coder", "daytona"]))

    def test_rejects_unknown_billing_mode(self):
        with self.assertRaisesRegex(ValueError, "AO_CLOUD_DEFAULT_ORG_BILLING"):
            validate_offering(**offering(default_org_billing="free"))

    def test_rejects_malformed_capability_or_feature(self):
        with self.assertRaisesRegex(ValueError, "DEFAULT_ORG_CAPABILITIES"):
            validate_offering(**offering(default_org_capabilities=["Free Style"]))
        with self.assertRaisesRegex(ValueError, "PAYWALLED_FEATURES"):
            validate_offering(**offering(paywalled_features=["multi repo"]))

    def test_accepts_empty_gate_and_capabilities(self):
        validate_offering(**offering(gated_providers=[], default_org_capabilities=[]))

    def test_idle_thresholds_are_zero_or_at_least_a_minute(self):
        validate_offering(**offering(idle_pause_threshold="1h", freestyle_idle_pause_threshold="0"))
        for bad in ("30s", "15", "soon", ""):
            with self.subTest(value=bad), self.assertRaisesRegex(
                ValueError, "AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD"
            ):
                validate_offering(**offering(freestyle_idle_pause_threshold=bad))

    def test_coder_rollout_flags_are_true_or_false(self):
        validate_offering(**offering(coder_readable_names="false", coder_worker_working_folder=" false "))
        for name in ("coder_readable_names", "coder_worker_working_folder"):
            for bad in ("", "yes", "1", "True"):
                with self.subTest(name=name, value=bad), self.assertRaisesRegex(
                    ValueError, "AO_CLOUD_CODER_.* must be true or false"
                ):
                    validate_offering(**offering(**{name: bad}))

    def test_checked_in_offering_files_validate(self):
        files = {
            name: offering_file(name) for name in ("staging", "production")
        }
        staging = validate_offering(**offering_arguments(files["staging"]))
        validate_offering(
            **offering_arguments(files["production"]), staging_providers=staging
        )
        self.assertNotIn("nodeops", files["staging"]["AO_CLOUD_SANDBOX_PROVIDERS"])
        self.assertEqual(files["staging"]["AO_CLOUD_SANDBOX_PROVIDER"], "coder")
        self.assertEqual(files["production"]["AO_CLOUD_SANDBOX_PROVIDER"], "coder")
        # The Coder rollout flags run in staging first; production turns them
        # on one release later, so its rollback target can read what they write.
        for name in ("AO_CLOUD_CODER_READABLE_NAMES", "AO_CLOUD_CODER_WORKER_WORKING_FOLDER"):
            self.assertEqual(files["staging"][name], "true")
            self.assertEqual(files["production"][name], "false")


def offering_file(environment):
    path = Path(__file__).parents[1] / "offering" / f"{environment}.env"
    values = {}
    for line in path.read_text().splitlines():
        if line.strip() and not line.lstrip().startswith("#"):
            name, _, value = line.partition("=")
            values[name] = value
    return values


def offering_arguments(values):
    def split(name):
        return [part for part in values[name].split(",") if part]

    return {
        "providers": split("AO_CLOUD_SANDBOX_PROVIDERS"),
        "default_provider": values["AO_CLOUD_SANDBOX_PROVIDER"],
        "gated_providers": split("AO_CLOUD_CAPABILITY_GATED_PROVIDERS"),
        "default_org_capabilities": split("AO_CLOUD_DEFAULT_ORG_CAPABILITIES"),
        "default_org_billing": values["AO_CLOUD_DEFAULT_ORG_BILLING"],
        "paywalled_features": split("AO_CLOUD_PAYWALLED_FEATURES"),
        "idle_pause_threshold": values["AO_CLOUD_IDLE_PAUSE_THRESHOLD"],
        "freestyle_idle_pause_threshold": values["AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD"],
        "coder_readable_names": values["AO_CLOUD_CODER_READABLE_NAMES"],
        "coder_worker_working_folder": values["AO_CLOUD_CODER_WORKER_WORKING_FOLDER"],
    }


def stripe_settings():
    return {
        "secret_key": "sk_test_abc",
        "webhook_secret": "whsec_abc",
        "price_ids": {"starter": "price_1", "pro": "price_2"},
        "portal_configuration": "bpc_1",
        "exempt_orgs_created_before": "2026-10-01T00:00:00Z",
    }


class StripeSettingsTests(unittest.TestCase):
    def test_accepts_complete_settings(self):
        validate_stripe_settings(stripe_settings(), environment="staging")

    def test_accepts_price_ids_as_json_string_and_optional_fields_absent(self):
        stripe = stripe_settings()
        stripe["price_ids"] = '{"starter":"price_1"}'
        del stripe["portal_configuration"]
        del stripe["exempt_orgs_created_before"]
        validate_stripe_settings(stripe, environment="staging")

    def test_rejects_missing_webhook_secret(self):
        stripe = stripe_settings()
        del stripe["webhook_secret"]
        with self.assertRaisesRegex(ValueError, "webhook_secret"):
            validate_stripe_settings(stripe, environment="staging")

    def test_rejects_missing_or_malformed_price_ids(self):
        for price_ids in (None, {}, {"starter": "prod_1"}, "not json"):
            stripe = stripe_settings()
            stripe["price_ids"] = price_ids
            with self.subTest(price_ids=price_ids), self.assertRaisesRegex(
                ValueError, "price"
            ):
                validate_stripe_settings(stripe, environment="staging")

    def test_rejects_key_from_the_wrong_mode(self):
        with self.assertRaisesRegex(ValueError, "live key"):
            validate_stripe_settings(stripe_settings(), environment="production")
        stripe = stripe_settings()
        stripe["secret_key"] = "sk_live_abc"
        validate_stripe_settings(stripe, environment="production")
        with self.assertRaisesRegex(ValueError, "must not be a live key"):
            validate_stripe_settings(stripe, environment="staging")

    def test_rejects_malformed_portal_or_cutoff(self):
        stripe = stripe_settings()
        stripe["portal_configuration"] = "portal"
        with self.assertRaisesRegex(ValueError, "bpc_"):
            validate_stripe_settings(stripe, environment="staging")
        for cutoff in ("2026-10-01", "yesterday", "2026-10-01T00:00:00"):
            stripe = stripe_settings()
            stripe["exempt_orgs_created_before"] = cutoff
            with self.subTest(cutoff=cutoff), self.assertRaisesRegex(ValueError, "RFC 3339"):
                validate_stripe_settings(stripe, environment="staging")


def stripe_secret_overrides(environment="staging"):
    return secret_environment(f"arn:secret:ao-cloud/{environment}/stripe", STRIPE_SECRET_ENV)


def render_coder(source, environment="staging", **kwargs):
    return build_task_definition(
        source,
        family=f"ao-cloud-{environment}-api",
        container_name="control-plane",
        image=CONTROL_IMAGE,
        release="rel",
        environment=environment,
        log_group=f"/ao-cloud/{environment}/control-plane",
        region="eu-north-1",
        worker_image=WORKER_IMAGE,
        sandbox_provider="coder",
        **kwargs,
    )


def rendered_names(payload):
    container = payload["containerDefinitions"][0]
    return (
        {item["name"]: item["value"] for item in container["environment"]},
        {item["name"]: item["valueFrom"] for item in container["secrets"]},
    )


class BillingAndOfferingTaskDefinitionTests(unittest.TestCase):
    def inherited_stripe_source(self, environment="staging"):
        source = task_source(environment)
        container = source["taskDefinition"]["containerDefinitions"][0]
        container["environment"] += [
            {"name": "AO_CLOUD_STRIPE_PRICE_IDS", "value": '{"pro":"price_1"}'},
            {"name": "AO_CLOUD_STRIPE_PORTAL_CONFIGURATION", "value": "bpc_1"},
            {"name": "AO_CLOUD_BILLING_EXEMPT_ORGS_CREATED_BEFORE", "value": "2026-01-01T00:00:00Z"},
            {"name": "AO_CLOUD_SANDBOX_DEFAULT_PROVIDER", "value": "nodeops"},
        ]
        container["secrets"] += [
            {"name": name, "valueFrom": value}
            for name, value in stripe_secret_overrides(environment).items()
        ]
        return source

    def test_removing_the_stripe_secret_strips_inherited_billing(self):
        payload = render_coder(
            self.inherited_stripe_source(),
            secret_overrides=coder_secret_overrides("staging"),
        )
        environment, secrets = rendered_names(payload)
        for name in (
            "AO_CLOUD_STRIPE_PRICE_IDS",
            "AO_CLOUD_STRIPE_PORTAL_CONFIGURATION",
            "AO_CLOUD_BILLING_EXEMPT_ORGS_CREATED_BEFORE",
            "AO_CLOUD_SANDBOX_DEFAULT_PROVIDER",
        ):
            self.assertNotIn(name, environment)
        self.assertFalse(set(STRIPE_SECRET_ENV) & secrets.keys())

    def test_renders_billing_when_the_deploy_sets_it(self):
        payload = render_coder(
            self.inherited_stripe_source(),
            environment_overrides={
                "AO_CLOUD_STRIPE_PRICE_IDS": '{"pro":"price_2"}',
                "AO_CLOUD_STRIPE_PORTAL_CONFIGURATION": "",
                "AO_CLOUD_BILLING_EXEMPT_ORGS_CREATED_BEFORE": "",
            },
            secret_overrides=coder_secret_overrides("staging") | stripe_secret_overrides(),
        )
        environment, secrets = rendered_names(payload)
        self.assertEqual(environment["AO_CLOUD_STRIPE_PRICE_IDS"], '{"pro":"price_2"}')
        # An empty override removes the name rather than rendering "".
        self.assertNotIn("AO_CLOUD_STRIPE_PORTAL_CONFIGURATION", environment)
        self.assertNotIn("AO_CLOUD_BILLING_EXEMPT_ORGS_CREATED_BEFORE", environment)
        self.assertTrue(set(STRIPE_SECRET_ENV) <= secrets.keys())

    def test_rejects_partial_billing(self):
        with self.assertRaisesRegex(ValueError, "needs both"):
            render_coder(
                task_source("staging"),
                environment_overrides={"AO_CLOUD_STRIPE_PRICE_IDS": '{"pro":"price_2"}'},
                secret_overrides=coder_secret_overrides("staging")
                | {"AO_CLOUD_STRIPE_SECRET_KEY": "arn:secret:ao-cloud/staging/stripe:secret_key::"},
            )
        with self.assertRaisesRegex(ValueError, "needs AO_CLOUD_STRIPE_PRICE_IDS"):
            render_coder(
                task_source("staging"),
                secret_overrides=coder_secret_overrides("staging") | stripe_secret_overrides(),
            )
        with self.assertRaisesRegex(ValueError, "without a Stripe secret"):
            render_coder(
                task_source("staging"),
                environment_overrides={"AO_CLOUD_STRIPE_PRICE_IDS": '{"pro":"price_2"}'},
                secret_overrides=coder_secret_overrides("staging"),
            )

    def test_support_token_is_managed_and_secret_only(self):
        source = task_source("staging")
        container = source["taskDefinition"]["containerDefinitions"][0]
        container["secrets"].append(
            {"name": "AO_CLOUD_SUPPORT_TOKEN", "valueFrom": "arn:secret:ao-cloud/staging/support:token::"}
        )
        # Inherited without the secret this deploy: stripped, not left dangling.
        _, secrets = rendered_names(render_coder(source, secret_overrides=coder_secret_overrides("staging")))
        self.assertNotIn("AO_CLOUD_SUPPORT_TOKEN", secrets)
        # Set by the deploy: rendered as a secret reference.
        _, secrets = rendered_names(render_coder(
            task_source("staging"),
            secret_overrides=coder_secret_overrides("staging")
            | {"AO_CLOUD_SUPPORT_TOKEN": "arn:secret:ao-cloud/staging/support:token::"},
        ))
        self.assertIn("AO_CLOUD_SUPPORT_TOKEN", secrets)
        with self.assertRaisesRegex(ValueError, "plaintext"):
            render_coder(
                task_source("staging"),
                environment_overrides={"AO_CLOUD_SUPPORT_TOKEN": "x" * 40},
                secret_overrides=coder_secret_overrides("staging"),
            )

    def test_rejects_stripe_key_as_plaintext(self):
        with self.assertRaisesRegex(ValueError, "plaintext"):
            render_coder(
                task_source("staging"),
                environment_overrides={"AO_CLOUD_STRIPE_SECRET_KEY": "sk_test_abc"},
                secret_overrides=coder_secret_overrides("staging"),
            )

    def test_production_coder_only_drops_nodeops_and_renders_offering(self):
        source = task_source("production")
        container = source["taskDefinition"]["containerDefinitions"][0]
        container["environment"].append({"name": "AO_CLOUD_SANDBOX_PROVIDERS", "value": "nodeops,coder"})
        container["secrets"] += [
            {"name": name, "valueFrom": value}
            for name, value in multi_provider_secret_overrides("production").items()
        ]
        payload = render_coder(
            source,
            environment="production",
            sandbox_providers=["coder"],
            environment_overrides={
                "AO_CLOUD_CAPABILITY_GATED_PROVIDERS": "coder,freestyle",
                "AO_CLOUD_DEFAULT_ORG_CAPABILITIES": "freestyle",
                "AO_CLOUD_DEFAULT_ORG_BILLING": "self_serve",
                "AO_CLOUD_PAYWALLED_FEATURES": "",
                "AO_CLOUD_IDLE_PAUSE_THRESHOLD": "0",
                "AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD": "15m",
            },
            secret_overrides=coder_secret_overrides("production"),
        )
        environment, secrets = rendered_names(payload)
        self.assertEqual(environment["AO_CLOUD_SANDBOX_PROVIDERS"], "coder")
        self.assertEqual(environment["AO_CLOUD_CAPABILITY_GATED_PROVIDERS"], "coder,freestyle")
        self.assertEqual(environment["AO_CLOUD_IDLE_PAUSE_THRESHOLD"], "0")
        self.assertNotIn("AO_CLOUD_PAYWALLED_FEATURES", environment)
        self.assertFalse(set(NODEOPS_SECRET_ENV) & secrets.keys())
        self.assertFalse(set(FREESTYLE_SECRET_ENV) & secrets.keys())

    def test_rejects_malformed_offering_values(self):
        for name, value in (
            ("AO_CLOUD_CAPABILITY_GATED_PROVIDERS", "coder,daytona"),
            ("AO_CLOUD_DEFAULT_ORG_BILLING", "free"),
            ("AO_CLOUD_IDLE_PAUSE_THRESHOLD", "30s"),
            ("AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD", "15"),
            ("AO_CLOUD_CODER_READABLE_NAMES", "yes"),
            ("AO_CLOUD_CODER_WORKER_WORKING_FOLDER", "on"),
        ):
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, name):
                render_coder(
                    task_source("staging"),
                    environment_overrides={name: value},
                    secret_overrides=coder_secret_overrides("staging"),
                )


def snapshot(harness, created, ident):
    return {
        "id": ident,
        "slug": f"ao-{harness}-{'c' * 12}-{created}",
        "createdAt": f"2026-10-{created[6:8]}T00:00:00Z",
    }


class FreestyleSnapshotPruneTests(unittest.TestCase):
    def listing(self):
        return {
            "snapshots": [
                snapshot("codex", "20261001000000", "codex-1"),
                snapshot("codex", "20261002000000", "codex-2"),
                snapshot("codex", "20261003000000", "codex-3"),
                snapshot("codex", "20261004000000", "codex-4"),
                snapshot("claude-code", "20261001000000", "claude-1"),
                {"id": "project-1", "slug": "ao-project-x", "createdAt": "2026-01-01T00:00:00Z"},
            ]
        }

    def test_keeps_the_two_newest_per_harness(self):
        stale = stale_harness_snapshots(self.listing(), ["claude-code", "codex", "cursor"], set())
        self.assertEqual(stale, ["codex-2", "codex-1"])

    def test_never_prunes_a_protected_snapshot(self):
        stale = stale_harness_snapshots(self.listing(), ["codex"], {"codex-1"})
        self.assertEqual(stale, ["codex-2"])

    def test_reads_snapshots_a_task_definition_boots_from(self):
        source = task_source("staging")
        source["taskDefinition"]["containerDefinitions"][0]["environment"].append(
            {
                "name": "AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS",
                "value": '{"codex":"codex-1","claude-code":"claude-1"}',
            }
        )
        self.assertEqual(
            task_definition_freestyle_snapshots(source), {"codex-1", "claude-1"}
        )
        self.assertEqual(task_definition_freestyle_snapshots(task_source()), set())

    def test_malformed_task_snapshot_map_fails_rather_than_unprotecting(self):
        source = task_source("staging")
        source["taskDefinition"]["containerDefinitions"][0]["environment"].append(
            {"name": "AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS", "value": "{not json"}
        )
        with self.assertRaises(ValueError):
            task_definition_freestyle_snapshots(source)


if __name__ == "__main__":
    unittest.main()
