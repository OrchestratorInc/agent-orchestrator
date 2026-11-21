import importlib.util
import io
import json
import re
from datetime import datetime, timedelta, timezone
import subprocess
import textwrap
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("sync_project", ROOT / "scripts" / "sync_project.py")
sync_project = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
sys.modules[SPEC.name] = sync_project
SPEC.loader.exec_module(sync_project)


REPO = "OrchestratorInc/agent-orchestrator"
ROUTING_FIELD_ID = "PVTF_routing"
EVIDENCE_FIELD_ID = "PVTF_evidence"
OWNER_FIELD_ID = "PVTF_owner"
RECORD_FIELD_ID = "PVTF_record"


def mapping():
    return {
        "repo": REPO,
        "reviewed_at": "2026-10-19T18:52:34Z",
        "areas": [
            {"id": "desktop-ui", "label": "Desktop UI"},
            {"id": "comma-area", "label": "Area, with comma"},
            {"id": "needs-area", "label": "Needs area review"},
        ],
        "issues": {
            "1": {"area": "desktop-ui", "routing_reason": "Reviewed UI report."},
        },
    }


def issue(number, node_id=None, labels=()):
    return sync_project.Issue(
        number=number,
        node_id=node_id or f"I_issue_{number}",
        html_url=f"https://github.com/OrchestratorInc/agent-orchestrator/issues/{number}",
        labels=labels,
    )


def connection(nodes):
    return {"nodes": nodes, "pageInfo": {"hasNextPage": False, "endCursor": None}}


def option(option_id, name, color="BLUE", description="Reviewed route"):
    return {"id": option_id, "name": name, "color": color, "description": description}


def response(payload, status=200, headers=""):
    return unittest.mock.Mock(returncode=int(status >= 400), stderr="",
                              stdout=f"HTTP/2.0 {status}\r\n{headers}\r\n" + json.dumps(payload))


def owners():
    return {"repo": REPO, "generated_at": "2026-10-23T00:00:00Z", "areas": {
        "desktop-ui": {"owners": [{"login": "alice"}, {"login": "bob"}], "unowned": False},
        "comma-area": {"owners": [], "unowned": True},
        "needs-area": {"owners": [], "unowned": True},
    }}


def project(items=None, include_evidence=True, include_owner=False, include_record=True):
    fields = [
        {
            "id": ROUTING_FIELD_ID,
            "name": "Suggested area",
            "dataType": "SINGLE_SELECT",
            "options": [
                option("OPT_UI", "Desktop UI"),
                option("OPT_COMMA", "Area, with comma"),
                option("OPT_NEEDS", "Needs area review"),
            ],
        }
    ]
    if include_evidence:
        fields.append({"id": EVIDENCE_FIELD_ID, "name": "Area evidence", "dataType": "TEXT"})
    if include_record:
        fields.append({"id": RECORD_FIELD_ID, "name": "Sync record", "dataType": "TEXT"})
    if include_owner:
        fields.append({"id": OWNER_FIELD_ID, "name": "Suggested owner", "dataType": "TEXT"})
    return sync_project.normalize_project_snapshot(
        {
            "id": "PVT_project",
            "url": "https://github.com/orgs/OrchestratorInc/projects/7",
            "fields": connection(fields),
            "items": connection(items or []),
        }
    )


def project_item(item_id, number, area="", evidence="", archived=False, repo=REPO, private_title=None, owner="", record=None, state="OPEN", kind="Issue"):
    content = {
        "__typename": kind,
        "state": state,
        "id": f"I_issue_{number}",
        "number": number,
        "url": f"https://github.com/{repo}/issues/{number}",
        "repository": {"nameWithOwner": repo},
    }
    if private_title is not None:
        content = {"__typename": "DraftIssue", "id": f"D_{item_id}", "title": private_title}
    values = []
    if area:
        values.append({"field": {"id": ROUTING_FIELD_ID, "name": "Suggested area"}, "optionId": "OPT_UI", "name": area})
    if evidence:
        values.append({"field": {"id": EVIDENCE_FIELD_ID, "name": "Area evidence"}, "text": evidence})
    if record is not None:
        values.append({"field": {"id": RECORD_FIELD_ID, "name": "Sync record"}, "text": json.dumps(record)})
    if owner:
        values.append({"field": {"id": OWNER_FIELD_ID, "name": "Suggested owner"}, "text": owner})
    return {
        "id": item_id,
        "isArchived": archived,
        "content": content,
        "fieldValues": connection(values),
    }


class ProjectSyncTests(unittest.TestCase):
    def test_evidence_field_is_never_created_or_filled(self):
        for snapshot in [project(), project(include_evidence=False)]:
            with self.subTest(snapshot=snapshot):
                plan = sync_project.build_plan(mapping(), [issue(1)], snapshot)
                self.assertFalse(any(op.field_name == "Area evidence" for op in plan.operations))
                self.assertFalse(any(field.name == "Area evidence" for field in plan.field_creates))

    def test_shared_infra_never_gets_an_owner_even_if_the_file_lists_one(self):
        mapped = mapping()
        mapped["areas"].append({"id": "shared-infra", "label": "Shared infrastructure"})
        mapped["issues"]["1"]["area"] = "shared-infra"
        people = owners()
        people["areas"]["shared-infra"] = {"owners": [{"login": "alice"}], "unowned": False}
        snapshot = project()
        snapshot.fields[0].options["Shared infrastructure"] = option("OPT_SHARED", "Shared infrastructure")
        plan = sync_project.build_plan(mapped, [issue(1)], snapshot, owners=people)
        self.assertFalse(any(op.field_name == "Suggested owner" for op in plan.operations))

    def test_added_and_existing_items_both_recheck_before_fill(self):
        snapshot = project([project_item("PVTI_existing", 2)])
        plan = sync_project.build_plan(mapping(), [issue(1), issue(2)], snapshot)
        responses = [
            {"addProjectV2ItemById": {"item": {"id": "PVTI_new"}}},
            {"node": {"isArchived": False, "syncRecord": None, "fieldValueByName": None}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_new"}}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_new"}}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_new"}}},
            {"node": {"isArchived": False, "syncRecord": None, "fieldValueByName": None}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_existing"}}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_existing"}}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_existing"}}},
        ]
        with patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
            result = sync_project.apply_plan(plan)
        self.assertEqual(result["items_added"], 1)
        self.assertEqual(result["fields_filled"], 2)
        rechecks = [call for call in graphql.call_args_list if call.args[0] == sync_project.FIELD_VALUE_QUERY]
        self.assertEqual([call.args[1]["itemId"] for call in rechecks], ["PVTI_new", "PVTI_existing"])

    def test_lagging_item_list_does_not_overwrite_human_value(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project())
        human = {"node": {"isArchived": False, "syncRecord": None, "fieldValueByName": {
            "name": "Shared infrastructure", "optionId": "OPT_SHARED",
            "field": {"id": ROUTING_FIELD_ID, "name": "Suggested area", "dataType": "SINGLE_SELECT"}}}}
        responses = [{"addProjectV2ItemById": {"item": {"id": "PVTI_already_there"}}}, human]
        with patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
            result = sync_project.apply_plan(plan)
        self.assertEqual(result["fields_filled"], 0)
        self.assertEqual(result["fields_skipped_after_recheck"], 1)
        self.assertEqual(graphql.call_count, 2)

    def test_missing_area_options_are_added_with_existing_ids_colors_and_descriptions(self):
        snapshot = project()
        snapshot.fields[0].options.pop("Area, with comma")
        plan = sync_project.build_plan(mapping(), [], snapshot)
        current = {"id": ROUTING_FIELD_ID, "name": "Suggested area", "dataType": "SINGLE_SELECT",
                   "options": list(snapshot.fields[0].options.values()) + [option("OPT_HUMAN", "Human option", "PINK", "Keep me")]}
        updated = {**current, "options": current["options"] + [option("OPT_NEW", "Area, with comma", "GRAY", "Contributor routing option")]}
        responses = [{"node": current}, {"updateProjectV2Field": {"projectV2Field": updated}}]
        with patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
            result = sync_project.apply_plan(plan)
        self.assertEqual(result["fields_updated"], 1)
        query, variables = graphql.call_args.args
        self.assertEqual(query, sync_project.UPDATE_OPTIONS_MUTATION)
        self.assertEqual(variables["input"]["singleSelectOptions"], current["options"] + [
            {"name": "Area, with comma", "color": "GRAY", "description": "Contributor routing option"}])
        converged = project()
        converged.fields[0] = sync_project._normalize_field(updated)
        self.assertEqual(sync_project.build_plan(mapping(), [], converged).field_updates, {})

    def test_option_added_after_plan_needs_no_mutation(self):
        field = project().fields[0]
        current = {"id": field.id, "name": field.name, "dataType": field.data_type, "options": list(field.options.values())}
        with patch.object(sync_project, "_graphql_payload", return_value={"node": current}) as graphql:
            result = sync_project._add_options(field, ("Desktop UI",))
        self.assertEqual(result, field)
        self.assertEqual(graphql.call_count, 1)

    def test_option_update_rejects_changed_existing_identity(self):
        field = project().fields[0]
        current = {"id": field.id, "name": field.name, "dataType": field.data_type, "options": list(field.options.values())}
        updated = {**current, "options": [option("REPLACED", "Desktop UI")] + current["options"][1:] + [option("NEW", "New area")]}
        responses = [{"node": current}, {"updateProjectV2Field": {"projectV2Field": updated}}]
        with patch.object(sync_project, "_graphql_payload", side_effect=responses), self.assertRaisesRegex(sync_project.SyncError, "changed an existing option"):
            sync_project._add_options(field, ("New area",))

    def test_rate_limits_retry_once_using_retry_after_or_sixty_seconds(self):
        for status, headers, delay in [(403, "Retry-After: 7\r\n", 7), (429, "", 60), (429, "retry-after: 2\r\n", 2)]:
            responses = [response({"message": "Rate limited"}, status, headers), response({"data": {"ok": True}})]
            with self.subTest(status=status, headers=headers), patch.object(sync_project.subprocess, "run", side_effect=responses) as run, patch.object(sync_project.time, "sleep") as sleep:
                self.assertEqual(sync_project._graphql_payload("query { ok }", {}), {"ok": True})
                self.assertEqual(run.call_count, 2)
                sleep.assert_called_once_with(delay)

    def test_retry_after_http_date(self):
        now = sync_project.datetime(2026, 10, 21, tzinfo=sync_project.timezone.utc)
        responses = [response({}, 429, "Retry-After: Wed, 21 Oct 2026 00:00:09 GMT\r\n"), response({"data": {}})]
        with patch.object(sync_project.subprocess, "run", side_effect=responses), patch.object(sync_project.time, "sleep") as sleep, patch.object(sync_project, "datetime") as clock:
            clock.now.return_value = now
            sync_project._graphql_payload("query { ok }", {})
        sleep.assert_called_once_with(9)

    def test_persistent_rate_limit_stops_after_one_retry(self):
        with patch.object(sync_project.subprocess, "run", return_value=response({"message": "Rate limited"}, 403, "x-ratelimit-remaining: 0\r\n")) as run, patch.object(sync_project.time, "sleep") as sleep, self.assertRaisesRegex(sync_project.RateLimitError, "HTTP 403"):
            sync_project._graphql_payload("query { ok }", {})
        self.assertEqual(run.call_count, 2)
        sleep.assert_called_once_with(60)

    def test_graphql_http_200_rate_limit_retries_once(self):
        for headers, delay in [("", 60), ("Retry-After: 3\r\n", 3)]:
            limited = response({"errors": [{"type": "RATE_LIMITED", "message": "API rate limit exceeded"}]}, headers=headers)
            with (
                self.subTest(headers=headers),
                patch.object(sync_project.subprocess, "run", side_effect=[limited, response({"data": {"ok": True}})]) as run,
                patch.object(sync_project.time, "sleep") as sleep,
            ):
                self.assertEqual(sync_project._graphql_payload("query { ok }", {}), {"ok": True})
                self.assertEqual(run.call_count, 2)
                sleep.assert_called_once_with(delay)

    def test_persistent_graphql_http_200_rate_limit_raises_rate_limit_error(self):
        limited = response({"errors": [{"type": "RATE_LIMITED", "message": "API rate limit exceeded"}]})
        with patch.object(sync_project.subprocess, "run", return_value=limited) as run, patch.object(sync_project.time, "sleep") as sleep, self.assertRaises(sync_project.RateLimitError):
            sync_project._graphql_payload("query { ok }", {})
        self.assertEqual(run.call_count, 2)
        sleep.assert_called_once_with(60)

    def test_403_rate_limit_signals_retry(self):
        for headers, message in [
            ("x-ratelimit-remaining: 0\r\n", "API rate limit exceeded"),
            ("", "You have exceeded a secondary rate limit."),
        ]:
            responses = [response({"message": message}, 403, headers), response({"data": {"ok": True}})]
            with self.subTest(headers=headers, message=message), patch.object(sync_project.subprocess, "run", side_effect=responses) as run, patch.object(sync_project.time, "sleep") as sleep:
                self.assertEqual(sync_project._graphql_payload("query { ok }", {}), {"ok": True})
                self.assertEqual(run.call_count, 2)
                sleep.assert_called_once_with(60)

    def test_403_without_rate_limit_signals_fails_immediately(self):
        for headers in ["", "x-ratelimit-remaining: 100\r\n"]:
            denied = response({"message": "Resource not accessible by integration"}, 403, headers)
            with (
                self.subTest(headers=headers),
                patch.object(sync_project.subprocess, "run", return_value=denied) as run,
                patch.object(sync_project.time, "sleep") as sleep, self.assertRaisesRegex(sync_project.SyncError, "HTTP 403.*not accessible"),
            ):
                sync_project._graphql_payload("query { ok }", {})
            self.assertEqual(run.call_count, 1)
            sleep.assert_not_called()

    def test_apply_rate_limit_returns_explicit_incomplete_summary(self):
        plan = sync_project.build_plan(mapping(), [issue(1), issue(2)], project())
        for limited in [
            response({"message": "Rate limited"}, 429),
            response({"message": "Rate limited"}, 403, "x-ratelimit-remaining: 0\r\n"),
            response({"errors": [{"type": "RATE_LIMITED", "message": "Rate limited"}]}),
        ]:
            responses = [response({"data": {"addProjectV2ItemById": {"item": {"id": "PVTI_1"}}}}), limited, limited]
            out, err = io.StringIO(), io.StringIO()
            with (
                self.subTest(response=limited.stdout),
                patch.object(sync_project, "load_mapping", return_value=mapping()),
                patch.object(sync_project, "load_owners", return_value=owners()),
                patch.object(sync_project, "fetch_open_issues", return_value=[issue(1), issue(2)]),
                patch.object(sync_project, "fetch_project", return_value=project()),
                patch.object(sync_project, "build_plan", return_value=plan),
                patch.object(sync_project.subprocess, "run", side_effect=responses) as run,
                patch.object(sync_project.time, "sleep") as sleep,
                patch.object(sync_project.sys, "stdout", out),
                patch.object(sync_project.sys, "stderr", err),
            ):
                exit_code = sync_project.main(["--project-url", "https://github.com/orgs/OrchestratorInc/projects/7", "--apply"])
            self.assertEqual(exit_code, 3)
            applied = json.loads(out.getvalue())["applied"]
            self.assertEqual((applied["items_added"], applied["fields_filled"], applied["complete"], applied["deferred_reason"]), (1, 0, False, "rate_limit"))
            self.assertIn("Incomplete Project sync", err.getvalue())
            self.assertEqual(run.call_count, 3)
            sleep.assert_called_once_with(60)

    def test_apply_rate_limit_counts_only_completed_creates_and_fills(self):
        snapshot = project([project_item("PVTI_1", 1), project_item("PVTI_2", 2)], include_owner=False)
        plan = sync_project.build_plan(mapping(), [issue(1), issue(2)], snapshot, owners=owners())
        created = sync_project.FieldInfo(OWNER_FIELD_ID, "Suggested owner", "TEXT", {})
        responses = [{"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}}, {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}}, sync_project.RateLimitError("rate limited")]
        with (
            patch.object(sync_project, "_create_field", return_value=created),
            patch.object(sync_project, "_can_fill_field", return_value={}),
            patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql,
            patch.object(sync_project.sys, "stderr", io.StringIO()) as err,
        ):
            summary = sync_project.apply_plan(plan)
        self.assertEqual((summary["fields_created"], summary["fields_filled"], summary["complete"]), (1, 1, False))
        self.assertEqual(graphql.call_count, 3)
        self.assertIn("Incomplete Project sync", err.getvalue())

    def test_rate_limit_during_field_setup_returns_partial_counts(self):
        plan = sync_project.build_plan(mapping(), [], project(), owners=owners())
        plan.fields = {}
        plan.field_creates.insert(0, sync_project.FieldCreate("Suggested area", "SINGLE_SELECT", ("Desktop UI",)))
        created = sync_project.FieldInfo(ROUTING_FIELD_ID, "Suggested area", "SINGLE_SELECT", {})
        with patch.object(sync_project, "_create_field", side_effect=[created, sync_project.RateLimitError("rate limited")]), patch.object(sync_project.sys, "stderr", io.StringIO()) as err:
            summary = sync_project.apply_plan(plan)
        self.assertEqual(summary["fields_created"], 1)
        self.assertFalse(summary["complete"])
        self.assertIn("Incomplete Project sync", err.getvalue())

    def test_transient_failures_retry_once_after_five_seconds(self):
        no_headers = unittest.mock.Mock(returncode=1, stdout="", stderr="read: can't assign requested address")
        internal = response({"errors": [{"message": "Something went wrong while executing your query on 2026-10-22T22:05:06Z."}]})
        timeout = sync_project.subprocess.TimeoutExpired(["gh"], 150)
        html_502 = unittest.mock.Mock(returncode=1, stderr="", stdout="HTTP/2.0 502\r\n\r\n<html>Bad gateway</html>")
        for name, failure in [("no headers", no_headers), ("internal", internal), ("html 502", html_502), ("timeout", timeout)]:
            with (
                self.subTest(failure=name),
                patch.object(sync_project.sys, "stderr", io.StringIO()) as err,
                patch.object(sync_project.subprocess, "run", side_effect=[failure, response({"data": {"ok": True}})]) as run,
                patch.object(sync_project.time, "sleep") as sleep,
            ):
                self.assertEqual(sync_project._graphql_payload("query { ok }", {}), {"ok": True})
                self.assertEqual(run.call_count, 2)
                sleep.assert_called_once_with(5.0)
                self.assertIn("retrying once in 5s", err.getvalue())

    def test_persistent_transient_failures_raise_after_one_retry(self):
        no_headers = unittest.mock.Mock(returncode=1, stdout="", stderr="connection reset")
        internal = response({"errors": [{"message": "Something went wrong while executing your query."}]})
        timeout = sync_project.subprocess.TimeoutExpired(["gh"], 150)
        for name, failure, message in [("no headers", no_headers, "no HTTP headers"), ("internal", internal, "Something went wrong"), ("timeout", timeout, "timed out")]:
            with (
                self.subTest(failure=name),
                patch.object(sync_project.subprocess, "run", side_effect=[failure, failure]) as run,
                patch.object(sync_project.time, "sleep"),
                patch.object(sync_project.sys, "stderr", io.StringIO()),
                self.assertRaisesRegex(sync_project.SyncError, message) as raised,
            ):
                sync_project._graphql_payload("query { ok }", {})
            self.assertNotIsInstance(raised.exception, sync_project.RateLimitError)
            self.assertEqual(run.call_count, 2)

    def test_rate_limit_and_transient_retries_do_not_share_a_budget(self):
        limited = response({"errors": [{"type": "RATE_LIMITED", "message": "API rate limit exceeded"}]})
        bad_gateway = response({"message": "Bad gateway"}, 502)
        for name, sequence in [("transient then limited", [bad_gateway, limited]), ("limited then transient", [limited, bad_gateway])]:
            with (
                self.subTest(order=name),
                patch.object(sync_project.subprocess, "run", side_effect=[*sequence, response({"data": {"ok": True}})]) as run,
                patch.object(sync_project.time, "sleep") as sleep,
                patch.object(sync_project.sys, "stderr", io.StringIO()),
            ):
                self.assertEqual(sync_project._graphql_payload("query { ok }", {}), {"ok": True})
                self.assertEqual(run.call_count, 3)
                self.assertEqual(sorted(call.args[0] for call in sleep.call_args_list), [5.0, 60])

    def test_persistent_server_error_fails_after_one_retry(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project())
        with (
            patch.object(sync_project, "run", side_effect=lambda args: sync_project.apply_plan(plan)),
            patch.object(sync_project.subprocess, "run", return_value=response({"message": "Internal Server Error"}, 500)) as run,
            patch.object(sync_project.time, "sleep") as sleep,
            patch.object(sync_project.sys, "stdout", io.StringIO()) as out,
            patch.object(sync_project.sys, "stderr", io.StringIO()) as err,
        ):
            exit_code = sync_project.main(["--apply"])
        self.assertNotEqual(exit_code, 0)
        self.assertEqual(out.getvalue(), "")
        self.assertIn("HTTP 500", err.getvalue())
        self.assertEqual(run.call_count, 2)
        sleep.assert_called_once_with(5.0)

    def test_apply_other_errors_still_exit_nonzero_without_retry(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project())
        for denied, message in [
            (response({"message": "Resource not accessible by integration"}, 403), "not accessible"),
            (response({"errors": [{"type": "FORBIDDEN", "message": "Token requires project scope"}]}), "project scope"),
        ]:
            with (
                self.subTest(message=message),
                patch.object(sync_project, "run", side_effect=lambda args: sync_project.apply_plan(plan)),
                patch.object(sync_project.subprocess, "run", return_value=denied) as run,
                patch.object(sync_project.time, "sleep") as sleep,
                patch.object(sync_project.sys, "stdout", io.StringIO()) as out,
                patch.object(sync_project.sys, "stderr", io.StringIO()) as err,
            ):
                exit_code = sync_project.main(["--apply"])
            self.assertNotEqual(exit_code, 0)
            self.assertEqual(out.getvalue(), "")
            self.assertIn(message, err.getvalue())
            self.assertEqual(run.call_count, 1)
            sleep.assert_not_called()

    def test_rest_pagination_also_retries_rate_limits(self):
        rows = [{"number": n, "node_id": f"I{n}", "html_url": f"https://github.com/{REPO}/issues/{n}"} for n in range(1, 102)]
        responses = [response({}, 429, "Retry-After: 1\r\n"), response(rows[:100]), response(rows[100:])]
        with patch.object(sync_project.subprocess, "run", side_effect=responses) as run, patch.object(sync_project.time, "sleep") as sleep:
            issues = sync_project.fetch_open_issues(REPO)
        self.assertEqual(len(issues), 101)
        self.assertEqual(run.call_args_list[0], run.call_args_list[1])
        self.assertTrue(run.call_args.args[0][-1].endswith("&page=2"))
        sleep.assert_called_once_with(1)

    def test_missing_or_invalid_owners_still_add_issues(self):
        for failure in ("missing", "invalid"):
            args = sync_project.make_parser().parse_args(["--owners-file", "/missing/owners.json",
                "--project-url", "https://github.com/users/Rishet11/projects/1"])
            with self.subTest(failure=failure), patch.object(sync_project, "load_owners", side_effect=sync_project.SyncError(failure)), patch.object(sync_project, "fetch_open_issues", return_value=[issue(1)]), patch.object(sync_project, "fetch_project", return_value=project()), patch.object(sync_project.sys, "stderr", io.StringIO()):
                result = sync_project.run(args)
            self.assertEqual(result["counts"]["items_to_add"], 1)
            self.assertEqual([op["field"] for op in result["operations"] if op.get("field")], ["Suggested area"])

    def test_non_graphql_fixture_shapes_are_rejected(self):
        raw = project_item("PVTI_1", 1)
        raw["archived"] = raw.pop("isArchived")
        with self.assertRaisesRegex(sync_project.SyncError, "isArchived"):
            sync_project._normalize_item(raw)
        with self.assertRaisesRegex(sync_project.SyncError, "nodes connections"):
            sync_project.normalize_project_snapshot({"id": "PVT", "url": "https://github.com/orgs/OrchestratorInc/projects/7", "fields": [], "items": []})

    def test_plans_owner_field_creation_and_top_logins(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project(), owners=owners())
        self.assertEqual([(field.name, field.data_type) for field in plan.field_creates], [("Suggested owner", "TEXT")])
        owner_fills = [op.value for op in plan.operations if op.field_name == "Suggested owner"]
        self.assertEqual(owner_fills, ["@alice, @bob"])

    def test_preserves_human_owner_and_skips_archived_items(self):
        snapshot = project([
            project_item("PVTI_1", 1, area="Desktop UI", evidence="Reviewed UI report.", owner="@human"),
            project_item("PVTI_2", 2, archived=True),
        ], include_owner=True)
        plan = sync_project.build_plan(mapping(), [issue(1), issue(2)], snapshot, owners=owners())
        self.assertEqual(plan.operations, [])
        self.assertEqual(plan.archived_source_item_count, 1)

    def test_no_owner_for_unowned_or_unknown_area(self):
        mapped = mapping()
        mapped["issues"]["2"] = {"area": "comma-area"}
        plan = sync_project.build_plan(mapped, [issue(2), issue(42)], project(), owners=owners())
        self.assertFalse(any(op.field_name == "Suggested owner" for op in plan.operations))

    def test_owner_follows_human_selected_area(self):
        bot_owner = {"Suggested owner": "@alice, @bob"}
        for human_area, mapped_area, owner, record, expected in [
            ("Desktop UI", "comma-area", "", {}, [("Suggested owner", "@alice, @bob")]),
            ("Needs area review", "desktop-ui", "@alice, @bob", bot_owner, [("Suggested owner", "")]),
            ("Custom area", "comma-area", "@alice, @bob", bot_owner, []),
        ]:
            with self.subTest(area=human_area):
                mapped = mapping()
                mapped["issues"]["1"]["area"] = mapped_area
                item = project_item("item", 1, area=human_area, owner=owner, record=record)
                plan = sync_project.build_plan(mapped, [issue(1)], project([item], include_owner=True), owners=owners())
                self.assertEqual([(op.field_name, op.value) for op in plan.operations], expected)

    def test_owner_field_must_be_text(self):
        snapshot = project(include_owner=True)
        snapshot.fields[-1].data_type = "SINGLE_SELECT"
        with self.assertRaisesRegex(sync_project.SyncError, "Suggested owner.*TEXT"):
            sync_project.build_plan(mapping(), [issue(1)], snapshot, owners=owners())

    def test_owner_recheck_preserves_late_human_value_and_archive(self):
        snapshot = project([project_item("PVTI_1", 1, area="Desktop UI", evidence="Reviewed UI report.")], include_owner=True)
        plan = sync_project.build_plan(mapping(), [issue(1)], snapshot, owners=owners())
        for node in [
            {"isArchived": False, "syncRecord": None, "fieldValueByName": {"field": {"id": OWNER_FIELD_ID, "name": "Suggested owner", "dataType": "TEXT"}, "text": "@human"}},
            {"isArchived": True, "syncRecord": None, "fieldValueByName": None},
        ]:
            with self.subTest(node=node), patch.object(sync_project, "_graphql_payload", return_value={"node": node}) as graphql:
                result = sync_project.apply_plan(plan)
            self.assertEqual(result["fields_filled"], 0)
            self.assertEqual(result["fields_skipped_after_recheck"], 1)
            self.assertEqual(graphql.call_count, 1)

    def test_apply_creates_owner_field_and_only_writes_project_text(self):
        snapshot = project([project_item("PVTI_1", 1, area="Desktop UI", evidence="Reviewed UI report.")])
        plan = sync_project.build_plan(mapping(), [issue(1)], snapshot, owners=owners())
        responses = [
            {"createProjectV2Field": {"projectV2Field": {"id": OWNER_FIELD_ID, "name": "Suggested owner", "dataType": "TEXT"}}},
            {"node": {"isArchived": False, "syncRecord": None, "fieldValueByName": None}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}},
        ]
        with patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
            result = sync_project.apply_plan(plan)
        self.assertEqual(result["fields_created"], 1)
        self.assertEqual(result["fields_filled"], 1)
        writes = [call for call in graphql.call_args_list if "input" in call.args[1]]
        self.assertEqual([call.args[0] for call in writes], [sync_project.CREATE_FIELD_MUTATION, sync_project.UPDATE_FIELD_MUTATION, sync_project.UPDATE_FIELD_MUTATION, sync_project.UPDATE_FIELD_MUTATION])
        self.assertEqual(writes[-2].args[1]["input"]["fieldId"], OWNER_FIELD_ID)
        self.assertEqual(writes[-2].args[1]["input"]["value"], {"text": "@alice, @bob"})

    def test_owner_rerun_has_no_fills(self):
        snapshot = project([project_item("PVTI_1", 1, area="Desktop UI", evidence="Reviewed UI report.", owner="@alice, @bob")], include_owner=True)
        self.assertEqual(sync_project.build_plan(mapping(), [issue(1)], snapshot, owners=owners()).operations, [])

    def test_owner_file_validation(self):
        invalid = [
            {**owners(), "repo": "someone/else"},
            {**owners(), "areas": {}},
            {**owners(), "areas": {**owners()["areas"], "desktop-ui": {"owners": [{"login": "bad@login"}], "unowned": False}}},
            {**owners(), "areas": {**owners()["areas"], "desktop-ui": {"owners": [{"login": "Alice"}, {"login": "alice"}], "unowned": False}}},
            {**owners(), "areas": {**owners()["areas"], "desktop-ui": {"owners": [], "unowned": False}}},
        ]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "owners.json"
            path.write_text(json.dumps(owners()))
            self.assertEqual(sync_project.load_owners(path, mapping()), owners())
            for data in invalid:
                path.write_text(json.dumps(data))
                with self.subTest(data=data), self.assertRaises(sync_project.SyncError):
                    sync_project.load_owners(path, mapping())

    def test_cli_offline_defaults_to_dry_run_with_owner_fills(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            files = {
                "mapping": mapping(), "owners": owners(),
                "issues": [{"number": 1, "node_id": "I1", "html_url": f"https://github.com/{REPO}/issues/1"}],
                "project": {"id": "PVT_test", "url": "https://github.com/orgs/OrchestratorInc/projects/7", "fields": connection([]), "items": connection([])},
            }
            argv = []
            for name, data in files.items():
                path = root / f"{name}.json"
                path.write_text(json.dumps(data))
                argv.extend([f"--{name}-file", str(path)])
            with patch.object(sync_project, "_graphql_payload", side_effect=AssertionError("offline run contacted GitHub")):
                result = sync_project.run(sync_project.make_parser().parse_args(argv))
        self.assertEqual(result["mode"], "dry-run")
        self.assertIn({"name": "Suggested owner", "data_type": "TEXT", "options": []}, result["field_creates"])
        self.assertEqual([op["value"] for op in result["operations"] if op.get("field") == "Suggested owner"], ["@alice, @bob"])

    def test_rejects_issue_url_from_another_repo_or_number(self):
        for url in ("https://github.com/someone/else/issues/1", f"https://github.com/{REPO}/issues/2"):
            with self.subTest(url=url), self.assertRaisesRegex(sync_project.SyncError, "invalid open issue metadata"):
                sync_project.normalize_open_issues([{"number": 1, "node_id": "I1", "html_url": url}], REPO)

    def test_archive_mutations_name_github_input_types(self):
        for mutation, name in [(sync_project.ARCHIVE_MUTATION, "archiveProjectV2Item"),
                               (sync_project.UNARCHIVE_MUTATION, "unarchiveProjectV2Item")]:
            with self.subTest(name=name):
                self.assertEqual(re.findall(r"\$input: (\w+)!", mutation), [name[0].upper() + name[1:] + "Input"])
                self.assertEqual(re.findall(r"(\w+)\(input:", mutation), [name])

    def test_graphql_archive_state_preserves_archived_issue(self):
        archived = project_item("PVTI_archived", 1)
        archived["isArchived"] = True
        plan = sync_project.build_plan(mapping(), [issue(1)], project([archived]))
        self.assertEqual(plan.operations, [])
        self.assertEqual(plan.archived_source_item_count, 1)

    def test_preserves_human_routing_evidence_and_other_item_fields(self):
        snapshot = project(
            [
                project_item("PVTI_human", 1, area="Human decision", evidence="Human provenance"),
                project_item("PVTI_private", 99, private_title="PRIVATE TITLE MUST NOT APPEAR"),
                project_item("PVTI_other", 88, repo="someone/else"),
            ]
        )
        plan = sync_project.build_plan(mapping(), [issue(1)], snapshot)
        self.assertEqual(plan.operations, [])
        rendered = json.dumps(sync_project.plan_to_json(plan, "dry-run"))
        self.assertNotIn("PRIVATE TITLE MUST NOT APPEAR", rendered)
        self.assertEqual(plan.preserved_item_count, 2)

    def test_skips_archived_source_item_and_routes_new_issue(self):
        snapshot = project([project_item("PVTI_archived", 1, archived=True)])
        plan = sync_project.build_plan(mapping(), [issue(1), issue(2)], snapshot)
        operations = [(op.kind, op.issue.number, op.field_name, op.value) for op in plan.operations]
        self.assertIn(("add", 2, None, None), operations)
        self.assertNotIn(("fill", 1, "Suggested area", "Desktop UI"), operations)
        self.assertEqual(plan.archived_source_item_count, 1)

    def test_unknown_issue_routes_to_needs_area(self):
        plan = sync_project.build_plan(mapping(), [issue(42)], project())
        fills = [op for op in plan.operations if op.kind == "fill"]
        self.assertEqual([(op.field_name, op.value) for op in fills], [("Suggested area", "Needs area review")])

    def test_rerun_has_no_duplicate_adds_or_fills(self):
        first = sync_project.build_plan(mapping(), [issue(1)], project())
        self.assertEqual([op.kind for op in first.operations], ["add", "fill"])
        after_apply = project([project_item("PVTI_1", 1, area="Desktop UI", evidence="Reviewed UI report.")])
        second = sync_project.build_plan(mapping(), [issue(1)], after_apply)
        self.assertEqual(second.operations, [])

    def test_missing_required_option_plans_an_addition(self):
        snapshot = project()
        snapshot.fields[0].options.pop("Area, with comma")
        plan = sync_project.build_plan(mapping(), [issue(1)], snapshot)
        self.assertEqual(plan.field_updates, {"Suggested area": ("Area, with comma",)})

    def test_wrong_routing_field_type_fails(self):
        snapshot = project()
        snapshot.fields[0].data_type = "TEXT"
        with self.assertRaisesRegex(sync_project.SyncError, "Suggested area.*SINGLE_SELECT"):
            sync_project.build_plan(mapping(), [issue(1)], snapshot)

    def test_new_field_plan_keeps_comma_area_as_one_option(self):
        snapshot = project(include_evidence=False)
        snapshot.fields = []
        plan = sync_project.build_plan(mapping(), [issue(2)], snapshot, owners=owners())
        self.assertEqual([spec.name for spec in plan.field_creates], ["Suggested area", "Suggested owner", "Sync record"])
        self.assertEqual(plan.field_creates[0].options, ("Desktop UI", "Area, with comma", "Needs area review"))
        self.assertEqual([op.field_name for op in plan.operations if op.kind == "fill"], ["Suggested area"])

    def test_apply_rejects_offline_project_fixture(self):
        with tempfile.TemporaryDirectory() as directory:
            project_path = Path(directory) / "project.json"
            issues_path = Path(directory) / "issues.json"
            project_path.write_text(json.dumps({"id": "PVT", "fields": connection([]), "items": connection([])}))
            issues_path.write_text(json.dumps([{"number": 1, "node_id": "I1", "html_url": "https://github.com/a/b/issues/1"}]))
            parser = sync_project.make_parser()
            args = parser.parse_args(
                [
                    "--project-file",
                    str(project_path),
                    "--issues-file",
                    str(issues_path),
                    "--apply",
                ]
            )
            with self.assertRaisesRegex(sync_project.SyncError, "cannot be used with --project-file"):
                sync_project.run(args)

    def test_graphql_request_uses_json_stdin(self):
        completed = response({"data": {"ok": True}})
        with patch.object(sync_project.subprocess, "run", return_value=completed) as run:
            self.assertEqual(sync_project._graphql_payload("query { ok }", {"x": "a,b"}), {"ok": True})
        command = run.call_args.args[0]
        self.assertEqual(command, ["gh", "api", "--include", "graphql", "--input", "-"])
        payload = json.loads(run.call_args.kwargs["input"])
        self.assertEqual(payload["variables"]["x"], "a,b")

    def test_graphql_errors_keep_actionable_permission_message(self):
        completed = response({"errors": [{"message": "Token requires read:project scope"}]})
        with patch.object(sync_project.subprocess, "run", return_value=completed), self.assertRaisesRegex(sync_project.SyncError, "read:project"):
            sync_project._graphql_payload("query { node { id } }", {})

    def test_apply_preserves_values_entered_after_the_plan(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project([project_item("PVTI_1", 1)]))
        responses = [
            {"node": {"isArchived": False, "syncRecord": None, "fieldValueByName": {"field": {"id": ROUTING_FIELD_ID, "name": "Suggested area", "dataType": "SINGLE_SELECT"}, "name": "Human choice", "optionId": "OPT_HUMAN"}}},
        ]
        with patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
            applied = sync_project.apply_plan(plan)
        self.assertEqual(applied["fields_filled"], 0)
        self.assertEqual(applied["fields_skipped_after_recheck"], 1)
        self.assertEqual(graphql.call_count, 1)

    def test_apply_skips_item_archived_after_the_plan(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project([project_item("PVTI_1", 1)]))
        with patch.object(sync_project, "_graphql_payload", return_value={"node": {"isArchived": True, "syncRecord": None, "fieldValueByName": None}}):
            applied = sync_project.apply_plan(plan)
        self.assertEqual(applied["fields_filled"], 0)
        self.assertEqual(applied["fields_skipped_after_recheck"], 1)

    def test_apply_fills_only_blank_fields(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project([project_item("PVTI_1", 1)]))
        responses = [
            {"node": {"isArchived": False, "syncRecord": None, "fieldValueByName": None}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}},
        ]
        with patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
            applied = sync_project.apply_plan(plan)
        self.assertEqual(applied["fields_filled"], 1)
        writes = [call.args[1]["input"] for call in graphql.call_args_list if "input" in call.args[1]]
        self.assertEqual([write["fieldId"] for write in writes], [RECORD_FIELD_ID, ROUTING_FIELD_ID, RECORD_FIELD_ID])

    def test_incomplete_field_value_page_stops_before_any_mutation(self):
        fields = project().fields
        raw_fields = [{"id": field.id, "name": field.name, "dataType": field.data_type, "options": list(field.options.values())} for field in fields]
        responses = [
            {"OWNER_PROJECT": {"projectV2": {"id": "PVT_project", "url": "https://github.com/orgs/OrchestratorInc/projects/7", "fields": {"nodes": raw_fields, "pageInfo": {"hasNextPage": False, "endCursor": None}}}}},
            {"node": {"items": {"nodes": [{"id": "PVTI_1", "fieldValues": {"nodes": [], "pageInfo": {"hasNextPage": True, "endCursor": "next"}}}], "pageInfo": {"hasNextPage": False, "endCursor": None}}}},
            {"node": {"fieldValues": {"nodes": []}}},
        ]
        ref = sync_project.parse_project_url("https://github.com/orgs/OrchestratorInc/projects/7")
        with patch.object(sync_project, "_graphql_payload", side_effect=responses), self.assertRaisesRegex(sync_project.SyncError, "incomplete paginated field-values page"):
            sync_project.fetch_project(ref)

    def test_classifier_path_similarity_and_fallback(self):
        mapped = mapping()
        mapped["areas"][0]["description"] = "Sidebar navigation panel layout"
        mapped["areas"][1]["description"] = "Database migrations SQLite schema"
        mapped["issues"]["1"]["routing_reason"] = "Sidebar navigation panel layout"
        classifier = sync_project.Classifier(mapped, rules=[
            {"glob": "frontend/sidebar*", "area": "desktop-ui"},
            {"glob": "*", "area": "needs-area"}])
        for title, body, expected in [
            ("", "frontend/sidebar.tsx", "desktop-ui"),
            ("", "./frontend/sidebar.tsx.", "desktop-ui"),
            ("SQLite database schema migrations", "", "comma-area"),
            ("unrecognized frobnicator", "", "needs-area"),
        ]:
            with self.subTest(title=title, body=body):
                self.assertEqual(classifier.classify(sync_project.Issue(99, "I99", "", title, body)), expected)
        with patch.object(classifier, "scores", return_value=("desktop-ui", 0.25, 0.07)):
            self.assertEqual(classifier.classify(sync_project.Issue(99, "", "", body="frontend/sidebar.tsx")), "desktop-ui")
            self.assertEqual(classifier.classify(sync_project.Issue(99, "", "", title="sidebar")), "needs-area")
        with patch.object(classifier, "scores", return_value=("desktop-ui", 0.99, 0.01)):
            self.assertEqual(classifier.classify(sync_project.Issue(99, "", "", body="frontend/sidebar.tsx")), "needs-area")

    def test_path_only_report_counts_unique_nonshared_matches(self):
        mapped = mapping()
        mapped["issues"] = {str(n): {"area": "desktop-ui"} for n in range(1, 6)}
        issues = [sync_project.Issue(n, "", "", body=body) for n, body in enumerate([
            "frontend/sidebar.tsx README.md", "backend/db.go", "frontend/sidebar.tsx backend/db.go", "unknown"], 1)]
        rules = [{"glob": "frontend/*", "area": "desktop-ui"}, {"glob": "backend/*", "area": "comma-area"},
                 {"glob": "*", "area": "shared-infra"}]
        result = sync_project.classifier_report(mapped, issues, rules)["path_only"]
        self.assertEqual(result, {"reviewed": 5, "with_text": 4, "assigned": 2, "correct": 1, "precision": 0.5, "coverage": 0.4})

    def test_reviewed_routes_win_over_labels_and_classifier(self):
        classifier = unittest.mock.Mock()
        classifier.classify.return_value = "comma-area"
        plan = sync_project.build_plan(mapping(), [issue(1, labels=("area:comma-area",))], project(), classifier=classifier)
        self.assertEqual([op.value for op in plan.operations if op.kind == "fill"], ["Desktop UI"])
        classifier.classify.assert_not_called()

    def test_open_issue_labels_are_kept_without_extra_requests(self):
        raw = {"number": 99, "node_id": "I99", "html_url": f"https://github.com/{REPO}/issues/99",
               "labels": [{"name": "bug"}, {"name": "ArEa:CoMmA-ArEa"}]}
        with patch.object(sync_project, "_api_request", return_value=[raw]) as api:
            issues = sync_project.fetch_open_issues(REPO)
        self.assertEqual(issues[0].labels, ("bug", "ArEa:CoMmA-ArEa"))
        self.assertEqual(api.call_count, 1)
        classifier = unittest.mock.Mock()
        classifier.classify.return_value = "desktop-ui"
        plan = sync_project.build_plan(mapping(), issues, project(), classifier=classifier)
        self.assertEqual([op.value for op in plan.operations if op.kind == "fill"], ["Area, with comma"])
        classifier.classify.assert_not_called()

    def test_single_valid_area_label_wins_over_classifier(self):
        for labels in [("area:comma-area",), ("area:unknown", "AREA:COMMA-AREA")]:
            with self.subTest(labels=labels):
                classifier = unittest.mock.Mock()
                classifier.classify.return_value = "desktop-ui"
                plan = sync_project.build_plan(mapping(), [issue(99, labels=labels)], project(), classifier=classifier)
                self.assertEqual([op.value for op in plan.operations if op.kind == "fill"], ["Area, with comma"])
                classifier.classify.assert_not_called()

    def test_unknown_reserved_and_multiple_area_labels_fall_through(self):
        mapped = mapping()
        mapped["areas"].append({"id": "shared-infra", "label": "Shared infrastructure"})
        for labels in [("area:unknown",), ("area:needs-area",), ("area:shared-infra",),
                       ("area:desktop-ui", "AREA:COMMA-AREA"), ("area:comma-area", "AREA:COMMA-AREA")]:
            with self.subTest(labels=labels):
                classifier = unittest.mock.Mock()
                classifier.classify.return_value = "desktop-ui"
                labeled = issue(99, labels=labels)
                plan = sync_project.build_plan(mapped, [labeled], project(), classifier=classifier)
                self.assertEqual([op.value for op in plan.operations if op.kind == "fill"], ["Desktop UI"])
                classifier.classify.assert_called_once_with(labeled)

    def test_later_area_label_refreshes_only_recorded_bot_value(self):
        classifier = unittest.mock.Mock()
        classifier.classify.return_value = "comma-area"
        before = sync_project.build_plan(mapping(), [issue(99)], project(), classifier=classifier)
        old_area = next(op.value for op in before.operations if op.kind == "fill")
        recorded = {"Suggested area": old_area}
        snapshot = project([
            project_item("bot", 99, area=old_area, record=recorded),
            project_item("human", 99, area="Human choice", record=recorded),
            project_item("unrecorded", 99, area=old_area),
        ])
        classifier.classify.reset_mock()
        labeled = issue(99, labels=("area:desktop-ui",))
        after = sync_project.build_plan(mapping(), [labeled], snapshot, classifier=classifier)
        self.assertEqual([(op.item_id, op.field_name, op.value) for op in after.operations],
                         [("bot", "Suggested area", "Desktop UI")])
        classifier.classify.assert_not_called()

    def test_classifier_leave_one_out_does_not_match_its_own_reason(self):
        mapped = mapping()
        mapped["issues"]["1"]["routing_reason"] = "uniqueword"
        report = sync_project.classifier_report(mapped, [], [])
        self.assertEqual((report["evaluated"], report["assigned"]), (1, 0))

    def test_bot_values_refresh_but_human_values_and_unrecorded_values_stay(self):
        recorded = {"Suggested area": "Needs area review", "Suggested owner": "@old"}
        snapshot = project([
            project_item("bot", 1, area="Needs area review", owner="@old", record=recorded),
            project_item("human", 1, area="Human choice", owner="@human", record=recorded),
            project_item("unrecorded", 1, area="Needs area review", owner="@old"),
        ], include_owner=True)
        plan = sync_project.build_plan(mapping(), [issue(1)], snapshot, owners=owners())
        self.assertEqual([(op.item_id, op.field_name, op.value) for op in plan.operations], [
            ("bot", "Suggested area", "Desktop UI"), ("bot", "Suggested owner", "@alice, @bob")])

    def test_refresh_rechecks_record_and_field_when_item_list_lags(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project())
        for current, record, expected in [
            ("Needs area review", {"Suggested area": "Needs area review"}, 1),
            ("Human choice", {"Suggested area": "Needs area review"}, 0),
            ("Needs area review", {"Suggested area": "Different previous write"}, 0),
            ("Needs area review", {}, 0),
        ]:
            node = {"isArchived": False, "fieldValueByName": {
                "field": {"id": ROUTING_FIELD_ID, "dataType": "SINGLE_SELECT"}, "name": current, "optionId": "OPT_NEEDS"},
                "syncRecord": {"field": {"id": RECORD_FIELD_ID, "dataType": "TEXT"}, "text": json.dumps(record)}}
            confirmation = {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "existing"}}}
            responses = [{"addProjectV2ItemById": {"item": {"id": "existing"}}}, {"node": node}]
            if expected:
                responses += [confirmation, confirmation, confirmation]
            with self.subTest(current=current, record=record), patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
                summary = sync_project.apply_plan(plan)
            self.assertEqual(summary["fields_filled"], expected)
            if expected:
                written = json.loads(graphql.call_args.args[1]["input"]["value"]["text"])
                self.assertEqual(written, {"Suggested area": "Desktop UI"})

    def test_record_write_preserves_the_other_field(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project([project_item("item", 1)]))
        stored = {"field": {"id": RECORD_FIELD_ID, "dataType": "TEXT"}, "text": json.dumps({"Suggested owner": "@human"})}
        confirm = {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "item"}}}
        with patch.object(sync_project, "_graphql_payload", side_effect=[
            {"node": {"isArchived": False, "fieldValueByName": None, "syncRecord": stored}}, confirm, confirm, confirm]) as graphql:
            sync_project.apply_plan(plan)
        self.assertEqual(json.loads(graphql.call_args.args[1]["input"]["value"]["text"]),
                         {"Suggested area": "Desktop UI", "Suggested owner": "@human"})

    def test_record_first_interruption_cannot_resume_nonblank_refreshes(self):
        intended = {"Suggested area": "Desktop UI", "Suggested owner": "@alice, @bob"}
        for name, old in [("Suggested area", "Needs area review"), ("Suggested owner", "@old")]:
            for current in (old, ""):
                with self.subTest(field=name, current=current):
                    values = {**intended, name: current}
                    snapshot = project([project_item("item", 1, area=values["Suggested area"],
                        owner=values["Suggested owner"], record=intended)], include_owner=True)
                    plan = sync_project.build_plan(mapping(), [issue(1)], snapshot, owners=owners())
                    # A kept nonblank area reads as human-set, so its unowned area clears the bot-written owner.
                    expected = [name] if not current else ["Suggested owner"] if name == "Suggested area" else []
                    self.assertEqual([op.field_name for op in plan.operations], expected)
                    field = next(f for f in snapshot.fields if f.name == name)
                    record_field = next(f for f in snapshot.fields if f.name == "Sync record")
                    value = None if not current else {"field": {"id": field.id, "dataType": field.data_type},
                        **({"name": current, "optionId": "OPT_NEEDS"} if field.data_type == "SINGLE_SELECT" else {"text": current})}
                    node = {"isArchived": False, "fieldValueByName": value,
                        "syncRecord": {"field": {"id": RECORD_FIELD_ID, "dataType": "TEXT"}, "text": json.dumps(intended)}}
                    with patch.object(sync_project, "_graphql_payload", return_value={"node": node}):
                        self.assertEqual(sync_project._can_fill_field("item", field, record_field),
                                         intended if not current else None)

    def test_missing_recheck_record_or_changed_record_field_stops_writes(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project([project_item("item", 1)]))
        for node in ({"isArchived": False, "fieldValueByName": None},
                     {"isArchived": False, "fieldValueByName": None, "syncRecord": {"field": {"id": "wrong"}, "text": "{}"}}):
            with self.subTest(node=node), patch.object(sync_project, "_graphql_payload", return_value={"node": node}) as graphql, self.assertRaises(sync_project.SyncError):
                sync_project.apply_plan(plan)
            self.assertEqual(graphql.call_count, 1)

    def test_unowned_area_clears_only_recorded_bot_owner(self):
        mapped = mapping()
        mapped["issues"]["1"]["area"] = "comma-area"
        item = project_item("item", 1, area="Area, with comma", owner="@old", record={"Suggested owner": "@old"})
        plan = sync_project.build_plan(mapped, [issue(1)], project([item], include_owner=True), owners=owners())
        self.assertEqual([(op.field_name, op.value) for op in plan.operations], [("Suggested owner", "")])
        with patch.object(sync_project, "_can_fill_field", return_value={"Suggested owner": "@old"}), patch.object(sync_project, "_graphql_payload", side_effect=[
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "item"}}},
            {"clearProjectV2ItemFieldValue": {"projectV2Item": {"id": "item"}}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "item"}}}]) as graphql:
            sync_project.apply_plan(plan)
        self.assertEqual(graphql.call_args_list[1].args[0], sync_project.CLEAR_FIELD_MUTATION)
        self.assertEqual(json.loads(graphql.call_args.args[1]["input"]["value"]["text"]), {"Suggested owner": ""})

    def test_closed_issue_archive_uses_content_state_and_preserves_other_content(self):
        items = [project_item("closed", 1, state="CLOSED"), project_item("unknown", 2, state=None),
                 project_item("archived", 3, state="CLOSED", archived=True),
                 project_item("other", 4, state="CLOSED", repo="other/repo"),
                 project_item("pr", 5, state="CLOSED", kind="PullRequest")]
        plan = sync_project.build_plan(mapping(), [], project(items))
        self.assertEqual([(op.kind, op.item_id) for op in plan.operations], [("archive", "closed")])
        node = self.archive_node("closed", 1, state="CLOSED", record={"Suggested owner": "@alice"})
        confirm = {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "closed"}}}
        with patch.object(sync_project, "_graphql_payload", side_effect=[
                {"node": node}, confirm, {"archiveProjectV2Item": {"item": {"id": "closed"}}}]) as graphql:
            result = sync_project.apply_plan(plan)
        self.assertEqual(result["items_archived"], 1)
        self.assertEqual(graphql.call_args.args[0], sync_project.ARCHIVE_MUTATION)
        record_write = graphql.call_args_list[1].args[1]["input"]
        self.assertEqual(json.loads(record_write["value"]["text"]),
                         {"Suggested owner": "@alice", "bot_archived": "true"})
        self.assertIn("number state repository", sync_project.PROJECT_ITEMS_QUERY)
        self.assertIn("archivedStates: $archivedStates", sync_project.PROJECT_ITEMS_QUERY)

    def archive_node(self, item_id, number, state="OPEN", archived=False, record=None, repo=REPO):
        stored = None if record is None else {"field": {"id": RECORD_FIELD_ID, "dataType": "TEXT"},
                                             "text": json.dumps(record)}
        return {"isArchived": archived, "syncRecord": stored,
                "content": {"__typename": "Issue", "number": number, "state": state,
                            "repository": {"nameWithOwner": repo}}}

    def test_hourly_run_unarchives_only_open_bot_archives(self):
        items = [project_item("bot", 1, area="Desktop UI", archived=True, record={"bot_archived": "true"}),
                 project_item("human", 2, archived=True),
                 project_item("closed", 3, archived=True, state="CLOSED", record={"bot_archived": "true"}),
                 project_item("unknown", 4, archived=True, state=None, record={"bot_archived": "true"})]
        plan = sync_project.build_plan(mapping(), [issue(1), issue(2)], project(items))
        self.assertEqual([(op.kind, op.item_id) for op in plan.operations], [("unarchive", "bot")])
        node = self.archive_node("bot", 1, archived=True,
                                 record={"bot_archived": "true", "Suggested area": "Desktop UI"})
        with patch.object(sync_project, "_graphql_payload", side_effect=[
                {"node": node}, {"unarchiveProjectV2Item": {"item": {"id": "bot"}}},
                {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "bot"}}}]) as graphql:
            result = sync_project.apply_plan(plan)
        self.assertEqual(result["items_unarchived"], 1)
        self.assertEqual(graphql.call_args_list[1].args[0], sync_project.UNARCHIVE_MUTATION)
        self.assertEqual(json.loads(graphql.call_args.args[1]["input"]["value"]["text"]),
                         {"Suggested area": "Desktop UI"})
        after = project([project_item("bot", 1, area="Desktop UI", record={"Suggested area": "Desktop UI"})])
        self.assertEqual(sync_project.build_plan(mapping(), [issue(1)], after).operations, [])

    def test_interrupted_archive_and_unarchive_recover_on_next_run(self):
        record = {"bot_archived": "true", "Suggested area": "Desktop UI"}
        for archived in (False, True):
            with self.subTest(archived=archived):
                snapshot = project([project_item("bot", 1, area="Desktop UI", archived=archived, record=record)])
                plan = sync_project.build_plan(mapping(), [issue(1)], snapshot)
                self.assertEqual([(op.kind, op.item_id) for op in plan.operations], [("unarchive", "bot")])
                node = self.archive_node("bot", 1, archived=archived, record=record)
                responses = [{"node": node}]
                if archived:
                    responses.append({"unarchiveProjectV2Item": {"item": {"id": "bot"}}})
                responses.append({"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "bot"}}})
                with patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
                    result = sync_project.apply_plan(plan)
                self.assertEqual(result["items_unarchived"], int(archived))
                self.assertEqual(json.loads(graphql.call_args.args[1]["input"]["value"]["text"]),
                                 {"Suggested area": "Desktop UI"})

    def test_archive_rechecks_preserve_late_human_archives_and_issue_changes(self):
        for kind, node in [
            ("archive", self.archive_node("bot", 1, state="CLOSED", archived=True)),
            ("archive", self.archive_node("bot", 1, state="OPEN")),
            ("unarchive", self.archive_node("bot", 1, archived=True)),
            ("unarchive", self.archive_node("bot", 1, state="CLOSED", archived=True, record={"bot_archived": "true"})),
            ("unarchive", self.archive_node("bot", 1, archived=True, repo="other/repo", record={"bot_archived": "true"})),
        ]:
            snapshot = project([project_item("bot", 1, area="Desktop UI", archived=kind == "unarchive",
                state="CLOSED" if kind == "archive" else "OPEN", record={"bot_archived": "true"})])
            plan = sync_project.build_plan(mapping(), [], snapshot)
            with self.subTest(kind=kind, node=node), patch.object(sync_project, "_graphql_payload", return_value={"node": node}) as graphql:
                result = sync_project.apply_plan(plan)
            self.assertEqual((result["items_archived"], result["items_unarchived"]), (0, 0))
            self.assertEqual(graphql.call_count, 1)

    def test_archive_does_not_run_when_record_write_fails(self):
        plan = sync_project.build_plan(mapping(), [], project([project_item("bot", 1, state="CLOSED")]))
        node = self.archive_node("bot", 1, state="CLOSED")
        with patch.object(sync_project, "_graphql_payload", side_effect=[{"node": node},
                sync_project.SyncError("record failed")]) as graphql, self.assertRaisesRegex(sync_project.SyncError, "record failed"):
            sync_project.apply_plan(plan)
        self.assertEqual(graphql.call_count, 2)
        self.assertNotEqual(graphql.call_args.args[0], sync_project.ARCHIVE_MUTATION)

    def test_record_field_is_created_and_must_be_text(self):
        snapshot = project(include_record=False)
        plan = sync_project.build_plan(mapping(), [], snapshot)
        self.assertEqual(plan.field_creates, [sync_project.FieldCreate("Sync record", "TEXT")])
        snapshot.fields.append(sync_project.FieldInfo(RECORD_FIELD_ID, "Sync record", "SINGLE_SELECT", {}))
        with self.assertRaisesRegex(sync_project.SyncError, "Sync record.*TEXT"):
            sync_project.build_plan(mapping(), [], snapshot)

    def test_open_issue_pages_are_sorted_and_duplicates_deduped(self):
        rows = [{"number": n, "node_id": f"I{n}", "html_url": f"https://github.com/{REPO}/issues/{n}"} for n in range(1, 101)]
        with patch.object(sync_project, "_api_request", side_effect=[rows, rows[-1:]]) as api:
            result = sync_project.fetch_open_issues(REPO)
        self.assertEqual(len(result), 100)
        self.assertTrue(all("sort=created&direction=asc" in call.args[0][0] for call in api.call_args_list))

    def test_owner_build_is_independent_and_only_successful_outputs_are_cached(self):
        workflow = (ROOT.parents[1] / "workflows/contributor-project.yml").read_text()
        owners_job = workflow.split("  owners:\n", 1)[1].split("  sync:\n", 1)[0]
        sync_job = workflow.split("  sync:\n", 1)[1]
        self.assertIn("group: ao-contributor-owners", owners_job)
        self.assertIn("group: ao-contributor-project", sync_job)
        self.assertNotIn("needs:", sync_job)
        self.assertNotRegex(workflow, r"(?m)^  issues:")
        self.assertIn("cron: '*/15 * * * *'", workflow)
        self.assertIn("if: steps.extract.outcome == 'success'", owners_job)
        self.assertIn("steps.extract.outcome == 'failure'", owners_job)
        save_step = owners_job.split("      - name: Save today's successful owner build\n", 1)[1]
        self.assertIn("steps.owner-build.outcome == 'success'", save_step)
        self.assertNotIn("steps.owner-build.conclusion", save_step)
        evidence_step = owners_job.split("      - name: Save refreshed PR evidence\n", 1)[1].split("      - name:", 1)[0]
        self.assertIn("steps.extract.outcome == 'failure'", evidence_step)
        for path in ("ownership-raw.json", "ownership-progress.json", "ownership-cache"):
            self.assertIn(path, evidence_step)
        self.assertIn("hashFiles(", save_step)

    def test_owner_cache_key_ignores_generated_python_bytecode(self):
        import hashlib
        workflow = (ROOT.parents[1] / "workflows/contributor-project.yml").read_text()
        patterns = re.findall(r"'([^']+)'", re.search(r"hashFiles\(([^)]+)\)", workflow)[1])
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            scripts = root / ".github/scripts/contributor-project/scripts"
            data = root / ".github/scripts/contributor-project/data"
            scripts.mkdir(parents=True)
            data.mkdir()
            (scripts / "build_owners.py").write_text("source")
            (scripts / "sync_project.py").write_text("sync source")
            (data / "mapping.json").write_text("{}")
            def digest():
                excluded = {p for pattern in patterns if pattern.startswith("!") for p in root.glob(pattern[1:])}
                files = sorted({p for pattern in patterns if not pattern.startswith("!")
                                for p in root.glob(pattern) if p.is_file()} - excluded)
                return hashlib.sha256(b"".join(p.read_bytes() for p in files)).hexdigest()
            before = digest()
            (scripts / "__pycache__").mkdir()
            (scripts / "__pycache__/owners.pyc").write_bytes(b"generated bytecode")
            self.assertEqual(digest(), before)
            (scripts / "sync_project.py").write_text("changed sync source")
            self.assertEqual(digest(), before)
            (scripts / "blame_snapshot.py").write_text("imported by build_owners")
            self.assertNotEqual(digest(), before)

    def test_failed_owner_build_preserves_cached_owners_or_leaves_blank(self):
        for cached in (True, False):
            with self.subTest(cached=cached), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "area-owners.json"
                if cached:
                    output.write_text(json.dumps(owners()))
                args = sync_project.make_parser().parse_args(["--owners-file", str(output),
                    "--project-url", "https://github.com/users/Rishet11/projects/1"])
                with patch.object(sync_project, "load_mapping", return_value=mapping()), patch.object(sync_project, "fetch_open_issues", return_value=[issue(1)]), patch.object(sync_project, "fetch_project", return_value=project()), patch.object(sync_project.sys, "stderr", io.StringIO()):
                    result = sync_project.run(args)
                self.assertEqual(result["counts"]["items_to_add"], 1)
                self.assertEqual(any(op.get("field") == "Suggested owner" for op in result["operations"]), cached)

    def test_workflow_surfaces_deferred_sync_but_preserves_other_failures(self):
        import os
        workflow = (ROOT.parents[1] / "workflows/contributor-project.yml").read_text()
        step = workflow.split("      - name: Sync issues and bot suggestions\n", 1)[1]
        command = textwrap.dedent(step.split("        run: |\n", 1)[1])
        for code in (0, 3, 4, 2):
            with self.subTest(code=code), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / "scripts").mkdir()
                (root / "research").mkdir()
                (root / "scripts/sync_project.py").write_text("import json\nprint(json.dumps({'applied': {'complete': " + str(code == 0) + "}}))\nraise SystemExit(" + str(code) + ")\n")
                summary = root / "summary.md"
                result = subprocess.run(["bash", "-e", "-c", command], cwd=root, env={**os.environ, "GITHUB_STEP_SUMMARY": str(summary), "PROJECT_URL": "fixture"}, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0 if code in (0, 3) else 1 if code == 4 else code)
                if code in (0, 3):
                    self.assertIn('"complete": ' + str(code == 0).lower(), summary.read_text())
                if code == 3:
                    self.assertIn("::warning::Project sync is incomplete", result.stdout)
                if code == 4:
                    self.assertIn("::error::Project sync deferred without writing anything", result.stdout)

    def test_owner_age_warning_uses_generated_at_with_seven_day_boundary(self):
        now = datetime(2026, 10, 24, tzinfo=timezone.utc)
        for age, expected in [(timedelta(days=6), False), (timedelta(days=7), False),
                              (timedelta(days=7, seconds=1), True)]:
            for generated in [(now - age).isoformat(), (now - age).isoformat().replace("+00:00", "Z")]:
                data = {**owners(), "generated_at": generated}
                with self.subTest(generated=generated), patch.object(sync_project.sys, "stderr", io.StringIO()) as stderr:
                    sync_project.warn_stale_owners(data, now)
                    self.assertEqual("::warning::" in stderr.getvalue(), expected)
                    if expected:
                        self.assertIn("older than 7 days", stderr.getvalue())

    def test_missing_or_invalid_owner_timestamp_warns_without_stopping_sync(self):
        for value in [None, "invalid", "2026-10-20", 123]:
            with self.subTest(value=value), patch.object(sync_project.sys, "stderr", io.StringIO()) as stderr:
                sync_project.warn_stale_owners({**owners(), "generated_at": value})
                self.assertIn("::warning::", stderr.getvalue())

    def test_run_warns_when_restored_owners_are_stale(self):
        args = sync_project.make_parser().parse_args(["--project-url", "https://github.com/orgs/OrchestratorInc/projects/7"])
        stale = {**owners(), "generated_at": "2020-01-01T00:00:00Z"}
        with patch.object(sync_project, "load_mapping", return_value=mapping()), \
             patch.object(sync_project, "load_owners", return_value=stale), \
             patch.object(sync_project, "fetch_open_issues", return_value=[issue(1)]), \
             patch.object(sync_project, "fetch_project", return_value=project(include_owner=True)), \
             patch.object(sync_project.sys, "stderr", io.StringIO()) as stderr:
            result = sync_project.run(args)
        self.assertIn("::warning::Owners in use are older than 7 days", stderr.getvalue())
        self.assertTrue(any(op.get("field") == "Suggested owner" for op in result["operations"]))


if __name__ == "__main__":
    unittest.main()
