import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parents[1]))

from lib.offering_monitoring import (  # noqa: E402
    NAMESPACE,
    alarm_definitions,
    dashboard_body,
    log_group,
    metric_filters,
)

ROOT = Path(__file__).parents[3]


class OfferingMonitoringTests(unittest.TestCase):
    def test_metric_filters_key_on_logged_activity_fields(self):
        filters = metric_filters("production")
        names = {definition["filterName"] for definition in filters}
        self.assertEqual(len(names), len(filters), "filter names must be unique")
        for definition in filters:
            self.assertEqual(definition["logGroupName"], log_group("production"))
            self.assertTrue(definition["filterPattern"].startswith("{"))
            transformation = definition["metricTransformations"][0]
            self.assertEqual(transformation["metricNamespace"], NAMESPACE)
            self.assertTrue(transformation["metricName"].startswith("production-"))
            # CloudWatch rejects a default value on a metric with dimensions.
            self.assertFalse("dimensions" in transformation and "defaultValue" in transformation)
        by_name = {definition["filterName"]: definition for definition in filters}
        ttr = by_name["ao-offering-production-time-to-ready"]["metricTransformations"][0]
        self.assertEqual(ttr["metricValue"], "$.activity.ttr_ms")
        self.assertEqual(ttr["dimensions"], {"Provider": "$.sandbox_provider"})

    def test_every_activity_kind_in_filters_exists_in_go(self):
        source = (ROOT / "cloud/internal/domain/activity.go").read_text()
        for definition in metric_filters("staging"):
            pattern = definition["filterPattern"]
            if "activity_kind" not in pattern:
                continue
            kind = pattern.split('activity_kind = "', 1)[1].split('"', 1)[0]
            self.assertIn(f'"{kind}"', source, f"{kind} is not a domain activity kind")

    def test_alarms_reference_defined_metrics(self):
        metrics = {
            definition["metricTransformations"][0]["metricName"] for definition in metric_filters("staging")
        }
        alarms = alarm_definitions("staging", "arn:aws:sns:eu-north-1:123:alerts")
        self.assertGreaterEqual(len(alarms), 7)
        for alarm in alarms:
            self.assertTrue(alarm["AlarmName"].startswith("ao-cloud-staging-offering-"))
            self.assertTrue(alarm["ActionsEnabled"])
            if "MetricName" in alarm:
                self.assertIn(alarm["MetricName"], metrics)
            for query in alarm.get("Metrics", []):
                if "MetricStat" in query:
                    self.assertIn(query["MetricStat"]["Metric"]["MetricName"], metrics)
        names = {alarm["AlarmName"] for alarm in alarms}
        self.assertIn("ao-cloud-staging-offering-stripe-webhook-failures", names)
        self.assertIn("ao-cloud-staging-offering-byo-bootstrap-failures", names)
        self.assertIn("ao-cloud-staging-offering-freestyle-start-success-rate", names)
        self.assertFalse(alarm_definitions("staging")[0]["ActionsEnabled"])

    def test_dashboard_has_funnel_latency_and_failure_widgets(self):
        dashboard = dashboard_body("eu-north-1", "production")
        titles = {widget["properties"]["title"] for widget in dashboard["widgets"]}
        for title in (
            "Signups and checkout",
            "Session start success %",
            "Time to ready (create) p50/p95",
            "BYO Coder bootstrap failures",
            "Recent session start failures",
        ):
            self.assertIn(title, titles)
        json.dumps(dashboard)

    def test_committed_definitions_are_current(self):
        directory = ROOT / "cloud/infra/monitoring"
        for environment in ("staging", "production"):
            committed = json.loads((directory / f"offering-{environment}-metricFilters.json").read_text())
            self.assertEqual(committed, json.loads(json.dumps(metric_filters(environment))))
            committed = json.loads((directory / f"offering-{environment}-dashboard.json").read_text())
            self.assertEqual(committed, json.loads(json.dumps(dashboard_body("eu-north-1", environment))))


if __name__ == "__main__":
    unittest.main()
