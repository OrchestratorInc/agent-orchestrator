"""Exercise the deploy scripts' command-line and shell helpers without AWS."""

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPTS = Path(__file__).parents[1]
COMMON = SCRIPTS / "lib" / "deploy-common.sh"


def run(*arguments, stdin=""):
    return subprocess.run(
        [sys.executable, *arguments],
        cwd=SCRIPTS.parent,
        input=stdin,
        capture_output=True,
        text=True,
    )


def bash(script, env=None):
    return subprocess.run(
        ["bash", "-c", f"set -euo pipefail\nsource {COMMON}\n{script}"],
        cwd=SCRIPTS.parent,
        capture_output=True,
        text=True,
        env=env,
    )


def offering_arguments(environment, providers, **extra):
    arguments = [
        str(SCRIPTS / "validate-hosted-settings.py"),
        "--offering",
        "--environment",
        environment,
        "--providers",
        providers,
        "--provider",
        "coder",
        "--capability-gated-providers",
        "coder,freestyle",
        "--default-org-capabilities",
        "freestyle",
        "--default-org-billing",
        "self_serve",
        "--paywalled-features",
        "",
        "--idle-pause-threshold",
        "0",
        "--freestyle-idle-pause-threshold",
        "15m",
    ]
    for name, value in extra.items():
        arguments += [f"--{name.replace('_', '-')}", value]
    return arguments


class ValidateHostedSettingsCLITests(unittest.TestCase):
    def test_production_offering_requires_the_staging_provider_set(self):
        result = run(*offering_arguments("production", "coder"))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("--staging-providers", result.stderr)

    def test_production_offering_accepts_a_subset_and_rejects_more(self):
        ok = run(*offering_arguments("production", "coder", staging_providers="coder,freestyle"))
        self.assertEqual(ok.returncode, 0, ok.stderr)
        bad = run(*offering_arguments("production", "coder,freestyle", staging_providers="coder"))
        self.assertNotEqual(bad.returncode, 0)
        self.assertIn("not exercised in staging: freestyle", bad.stderr)

    def test_staging_offering(self):
        result = run(*offering_arguments("staging", "coder,freestyle"))
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_stripe_secret(self):
        with tempfile.NamedTemporaryFile("w", suffix=".json") as handle:
            json.dump({"secret_key": "sk_test_1", "webhook_secret": "whsec_1", "price_ids": {}}, handle)
            handle.flush()
            result = run(
                str(SCRIPTS / "validate-hosted-settings.py"),
                "--stripe",
                handle.name,
                "--environment",
                "staging",
            )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("price_ids", result.stderr)
        self.assertNotIn("sk_test_1", result.stderr)


class RenderTaskDefinitionCLITests(unittest.TestCase):
    def source(self):
        return json.dumps(
            {
                "taskDefinition": {
                    "taskRoleArn": "arn:task:staging",
                    "executionRoleArn": "arn:execution:staging",
                    "containerDefinitions": [
                        {
                            "name": "migration",
                            "image": "old",
                            "environment": [{"name": "KEEP", "value": "1"}],
                            "logConfiguration": {"options": {}},
                        }
                    ],
                }
            }
        )

    def render(self, *extra):
        return run(
            str(SCRIPTS / "render-task-definition.py"),
            "--family",
            "ao-cloud-staging-migrate",
            "--container",
            "migration",
            "--image",
            f"registry/ao-cloud-control-plane@sha256:{'a' * 64}",
            "--release",
            "rel",
            "--environment",
            "staging",
            "--log-group",
            "/ao-cloud/staging/control-plane",
            "--region",
            "eu-north-1",
            "--sandbox-provider",
            "coder",
            *extra,
            stdin=self.source(),
        )

    def test_empty_environment_assignment_removes_the_name(self):
        result = self.render("--set-environment", "KEEP=")
        self.assertEqual(result.returncode, 0, result.stderr)
        names = {item["name"] for item in json.loads(result.stdout)["containerDefinitions"][0]["environment"]}
        self.assertNotIn("KEEP", names)

    def test_empty_secret_assignment_is_rejected(self):
        result = self.render("--set-secret", "AO_CLOUD_STRIPE_SECRET_KEY=")
        self.assertNotEqual(result.returncode, 0)


class FreestyleStaleSnapshotsCLITests(unittest.TestCase):
    def test_protects_task_definition_snapshots(self):
        listing = {
            "snapshots": [
                {"id": f"codex-{day}", "slug": f"ao-codex-{'c' * 12}-202610{day:02d}000000", "createdAt": f"2026-10-{day:02d}"}
                for day in range(1, 5)
            ]
        }
        task = {
            "taskDefinition": {
                "containerDefinitions": [
                    {
                        "name": "control-plane",
                        "environment": [
                            {"name": "AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS", "value": '{"codex":"codex-1"}'}
                        ],
                    }
                ]
            }
        }
        with tempfile.NamedTemporaryFile("w", suffix=".json") as handle:
            json.dump(task, handle)
            handle.flush()
            result = run(
                str(SCRIPTS / "freestyle-stale-snapshots.py"),
                "--harnesses",
                "claude-code,codex",
                "--protect",
                "",
                "--protect-task-definition",
                handle.name,
                stdin=json.dumps(listing),
            )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.split(), ["codex-2"])


class DeployCommonShellTests(unittest.TestCase):
    def aws_stub(self, status, stderr="", stdout=""):
        return (
            "aws_cli() {\n"
            f"  printf '%s' {json.dumps(stdout)}\n"
            f"  printf '%s' {json.dumps(stderr)} >&2\n"
            f"  return {status}\n"
            "}\n"
        )

    def test_optional_secret_absent_is_empty(self):
        result = bash(
            self.aws_stub(254, "An error occurred (ResourceNotFoundException) when calling")
            + 'arn="$(optional_secret_arn ao-cloud/staging/stripe)"; echo "arn=[$arn]"'
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("arn=[]", result.stdout)

    def test_optional_secret_other_errors_fail(self):
        result = bash(
            self.aws_stub(254, "An error occurred (AccessDeniedException) when calling")
            + 'arn="$(optional_secret_arn ao-cloud/staging/stripe)"; echo reached'
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("reached", result.stdout)
        self.assertIn("AccessDeniedException", result.stderr)

    def test_optional_secret_present_prints_arn(self):
        result = bash(
            self.aws_stub(0, stdout="arn:aws:secretsmanager:x:stripe")
            + 'arn="$(optional_secret_arn ao-cloud/staging/stripe)"; echo "arn=[$arn]"'
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("arn=[arn:aws:secretsmanager:x:stripe]", result.stdout)

    def test_load_offering_respects_the_shell_including_empty(self):
        result = bash(
            "AO_CLOUD_CAPABILITY_GATED_PROVIDERS=\n"
            "AO_CLOUD_SANDBOX_PROVIDERS=coder\n"
            "load_offering ./scripts/offering/staging.env\n"
            'echo "gated=[$AO_CLOUD_CAPABILITY_GATED_PROVIDERS] providers=[$AO_CLOUD_SANDBOX_PROVIDERS] default=[$AO_CLOUD_SANDBOX_PROVIDER]"'
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("gated=[] providers=[coder] default=[coder]", result.stdout)

    def test_load_offering_rejects_unknown_settings(self):
        with tempfile.NamedTemporaryFile("w", suffix=".env") as handle:
            handle.write("AO_CLOUD_SANDBOX_PROVIDERS=coder\nAO_CLOUD_EVIL=$(touch /tmp/x)\n")
            handle.flush()
            result = bash(f"load_offering {handle.name}; echo reached")
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("reached", result.stdout)
        self.assertIn("unknown setting AO_CLOUD_EVIL", result.stderr)

    def test_deployment_configuration(self):
        result = bash(
            'deployment_configuration alarm-a false; echo; deployment_configuration alarm-b true'
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        default, rolling = (json.loads(line) for line in result.stdout.splitlines())
        self.assertEqual((default["maximumPercent"], default["minimumHealthyPercent"]), (100, 0))
        self.assertEqual((rolling["maximumPercent"], rolling["minimumHealthyPercent"]), (200, 100))
        self.assertEqual(rolling["alarms"]["alarmNames"], ["alarm-b"])
        self.assertTrue(rolling["deploymentCircuitBreaker"]["rollback"])


class ShellSyntaxTests(unittest.TestCase):
    def test_every_script_parses(self):
        scripts = sorted(SCRIPTS.glob("*.sh")) + sorted((SCRIPTS / "lib").glob("*.sh"))
        self.assertTrue(scripts)
        for script in scripts:
            with self.subTest(script=script.name):
                result = subprocess.run(["bash", "-n", str(script)], capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
