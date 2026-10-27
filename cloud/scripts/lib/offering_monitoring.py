#!/usr/bin/env python3
"""CloudWatch metric filters, alarms and a dashboard for the AO Cloud offering.

Every metric here is derived from the control plane's structured JSON logs:
the "activity event" lines the activity recorder writes (field activity_kind,
attributes under activity.*) and the trace fields logctx stamps on every line
(org_id, user_id, session_id, sandbox_provider, connection_id). The field
names are the contract in cloud/internal/logctx and cloud/internal/domain/
activity.go; cloud/docs/support-runbook.md documents them for operators.
"""

from __future__ import annotations

from typing import Any

NAMESPACE = "AOCloud/Offering"
PROVIDERS = ("freestyle", "coder", "docker")


def log_group(environment: str) -> str:
    return f"/ao-cloud/{environment}/control-plane"


def _activity(kind: str, extra: str = "") -> str:
    pattern = f'$.activity_kind = "{kind}"'
    if extra:
        pattern += f" && {extra}"
    return "{ " + pattern + " }"


def metric_filters(environment: str) -> list[dict[str, Any]]:
    """put-metric-filter inputs for one environment's control-plane log group."""
    group = log_group(environment)
    by_provider = {"Provider": "$.sandbox_provider"}

    def definition(
        name: str,
        pattern: str,
        metric: str,
        value: str = "1",
        dimensions: dict[str, str] | None = None,
        unit: str = "Count",
    ) -> dict[str, Any]:
        transformation: dict[str, Any] = {
            "metricName": metric,
            "metricNamespace": NAMESPACE,
            "metricValue": value,
            "unit": unit,
        }
        if dimensions:
            transformation["dimensions"] = dimensions
        else:
            transformation["defaultValue"] = 0
        return {
            "logGroupName": group,
            "filterName": f"ao-offering-{environment}-{name}",
            "filterPattern": pattern,
            "metricTransformations": [transformation],
        }

    return [
        definition("signups", _activity("org.created"), f"{environment}-Signups"),
        definition("checkout-started", _activity("billing.checkout_started"), f"{environment}-CheckoutStarted"),
        definition("checkout-completed", _activity("billing.checkout_completed"), f"{environment}-CheckoutCompleted"),
        definition("plan-refused", _activity("billing.plan_refused"), f"{environment}-PlanRefused"),
        definition("limit-hit", _activity("billing.limit_hit"), f"{environment}-LimitHit"),
        definition(
            "session-requested",
            _activity("session.create_requested"),
            f"{environment}-SessionCreateRequested",
            dimensions=by_provider,
        ),
        definition(
            "sandbox-ready-create",
            _activity("sandbox.ready", '$.activity.phase = "create"'),
            f"{environment}-SandboxReady",
            dimensions=by_provider,
        ),
        definition(
            "time-to-ready",
            _activity("sandbox.ready", '$.activity.phase = "create"'),
            f"{environment}-TimeToReady",
            value="$.activity.ttr_ms",
            dimensions=by_provider,
            unit="Milliseconds",
        ),
        definition(
            "time-to-resume",
            _activity("sandbox.ready", '$.activity.phase = "resume"'),
            f"{environment}-TimeToResume",
            value="$.activity.ttr_ms",
            dimensions=by_provider,
            unit="Milliseconds",
        ),
        definition(
            "session-start-failed",
            _activity("session.start_failed"),
            f"{environment}-SessionStartFailed",
            dimensions=by_provider,
        ),
        definition(
            "byo-bootstrap-failed",
            _activity("session.start_failed", "$.activity.customer_owned IS TRUE"),
            f"{environment}-BYOBootstrapFailed",
        ),
        definition(
            "freestyle-errors",
            '{ $.sandbox_provider = "freestyle" && ($.level = "ERROR" || $.level = "WARN") }',
            f"{environment}-FreestyleErrors",
        ),
        definition(
            "stripe-webhook-failures",
            '{ $.msg = "Stripe webhook failed" }',
            f"{environment}-StripeWebhookFailures",
        ),
        definition(
            "stripe-request-failures",
            '{ $.msg = "Stripe request failed" }',
            f"{environment}-StripeRequestFailures",
        ),
        definition(
            "activity-dropped",
            '{ $.msg = "activity queue full; dropping events" || $.msg = "record activity event" }',
            f"{environment}-ActivityWriteProblems",
        ),
    ]


def alarm_definitions(environment: str, alert_topic_arn: str = "") -> list[dict[str, Any]]:
    """put-metric-alarm inputs for one environment."""
    prefix = f"ao-cloud-{environment}-offering"
    actions = [alert_topic_arn] if alert_topic_arn else []
    common = {
        "ActionsEnabled": bool(actions),
        "AlarmActions": actions,
        "OKActions": actions,
        "TreatMissingData": "notBreaching",
    }

    def simple(
        name: str,
        description: str,
        metric: str,
        threshold: float,
        *,
        period: int = 300,
        evaluation_periods: int = 1,
        statistic: str = "Sum",
        dimensions: list[dict[str, str]] | None = None,
        extended: str = "",
    ) -> dict[str, Any]:
        alarm: dict[str, Any] = {
            "AlarmName": f"{prefix}-{name}",
            "AlarmDescription": description + " See cloud/docs/support-runbook.md.",
            "Namespace": NAMESPACE,
            "MetricName": f"{environment}-{metric}",
            "Dimensions": dimensions or [],
            "Period": period,
            "EvaluationPeriods": evaluation_periods,
            "Threshold": threshold,
            "ComparisonOperator": "GreaterThanOrEqualToThreshold",
        }
        if extended:
            alarm["ExtendedStatistic"] = extended
        else:
            alarm["Statistic"] = statistic
        return {**alarm, **common}

    alarms = [
        simple("stripe-webhook-failures", "Stripe webhooks are failing; subscriptions may not apply.",
               "StripeWebhookFailures", 1),
        simple("byo-bootstrap-failures", "A bring-your-own Coder session failed to start.",
               "BYOBootstrapFailed", 1, period=900),
        simple("freestyle-errors", "Sustained Freestyle provider errors in the reconciler.",
               "FreestyleErrors", 20, evaluation_periods=2),
        simple("activity-write-problems", "The activity trail is dropping or failing writes.",
               "ActivityWriteProblems", 5, period=900),
        simple("freestyle-time-to-ready-p95", "Freestyle p95 time-to-ready above 60 seconds.",
               "TimeToReady", 60000, period=900, evaluation_periods=2,
               dimensions=[{"Name": "Provider", "Value": "freestyle"}], extended="p95"),
    ]
    for provider in ("freestyle", "coder"):
        dimension = [{"Name": "Provider", "Value": provider}]
        alarms.append({
            "AlarmName": f"{prefix}-{provider}-start-success-rate",
            "AlarmDescription": f"Under 80% of {provider} sessions reached ready over an hour."
                                " See cloud/docs/support-runbook.md.",
            "Metrics": [
                {"Id": "ready", "ReturnData": False, "MetricStat": {
                    "Metric": {"Namespace": NAMESPACE, "MetricName": f"{environment}-SandboxReady",
                               "Dimensions": dimension},
                    "Period": 3600, "Stat": "Sum"}},
                {"Id": "requested", "ReturnData": False, "MetricStat": {
                    "Metric": {"Namespace": NAMESPACE, "MetricName": f"{environment}-SessionCreateRequested",
                               "Dimensions": dimension},
                    "Period": 3600, "Stat": "Sum"}},
                # Only judge an hour with real traffic: below 5 requests the
                # expression is empty and the alarm treats it as not breaching.
                {"Id": "rate", "ReturnData": True, "Label": "start success %",
                 "Expression": "IF(requested >= 5, 100 * ready / requested)"},
            ],
            "EvaluationPeriods": 1,
            "Threshold": 80,
            "ComparisonOperator": "LessThanThreshold",
            **common,
        })
    return alarms


def dashboard_body(region: str, environment: str) -> dict[str, Any]:
    """The ao-cloud-offering-<environment> dashboard."""

    def metric(name: str, *dimensions: str) -> list[str]:
        return [NAMESPACE, f"{environment}-{name}", *dimensions]

    def widget(title: str, x: int, y: int, metrics: list[Any], *, stat: str = "Sum",
               period: int = 3600, width: int = 8) -> dict[str, Any]:
        return {
            "type": "metric", "x": x, "y": y, "width": width, "height": 6,
            "properties": {
                "title": title, "region": region, "view": "timeSeries", "stacked": False,
                "metrics": metrics, "stat": stat, "period": period,
            },
        }

    funnel: list[Any] = []
    rates: list[Any] = []
    latency: list[Any] = []
    for index, provider in enumerate(PROVIDERS):
        requested, ready, failed = f"req{index}", f"ready{index}", f"fail{index}"
        funnel.extend([
            metric("SessionCreateRequested", "Provider", provider) + [{"id": requested, "label": f"{provider} requested"}],
            metric("SandboxReady", "Provider", provider) + [{"id": ready, "label": f"{provider} ready"}],
            metric("SessionStartFailed", "Provider", provider) + [{"id": failed, "label": f"{provider} failed"}],
        ])
        rates.extend([
            metric("SessionCreateRequested", "Provider", provider) + [{"id": requested, "visible": False}],
            metric("SandboxReady", "Provider", provider) + [{"id": ready, "visible": False}],
            [{"expression": f"IF({requested} > 0, 100 * {ready} / {requested})",
              "label": f"{provider} start success %", "id": f"rate{index}"}],
        ])
        latency.extend([
            metric("TimeToReady", "Provider", provider) + [{"stat": "p50", "label": f"{provider} p50"}],
            metric("TimeToReady", "Provider", provider) + [{"stat": "p95", "label": f"{provider} p95"}],
        ])
    insights = {
        "type": "log", "x": 0, "y": 18, "width": 24, "height": 6,
        "properties": {
            "title": "Recent session start failures",
            "region": region,
            "view": "table",
            "query": (
                f"SOURCE '{log_group(environment)}' | fields @timestamp, org_id, session_id, sandbox_provider,"
                " connection_id, activity.code, activity.stage, activity.message"
                ' | filter activity_kind = "session.start_failed" | sort @timestamp desc | limit 50'
            ),
        },
    }
    return {"widgets": [
        widget("Signups and checkout", 0, 0, [
            metric("Signups"), metric("CheckoutStarted"), metric("CheckoutCompleted"),
            metric("PlanRefused"), metric("LimitHit"),
        ]),
        widget("Session funnel by provider", 8, 0, funnel),
        widget("Session start success %", 16, 0, rates),
        widget("Time to ready (create) p50/p95", 0, 6, latency, period=900),
        widget("Time to resume p50/p95", 8, 6, [
            metric("TimeToResume", "Provider", provider) + [{"stat": stat, "label": f"{provider} {stat}"}]
            for provider in PROVIDERS for stat in ("p50", "p95")
        ], period=900),
        widget("Provider and billing errors", 16, 6, [
            metric("FreestyleErrors"), metric("StripeWebhookFailures"), metric("StripeRequestFailures"),
            metric("BYOBootstrapFailed"), metric("ActivityWriteProblems"),
        ], period=300),
        widget("BYO Coder bootstrap failures", 0, 12, [metric("BYOBootstrapFailed")], period=900, width=24),
        insights,
    ]}
