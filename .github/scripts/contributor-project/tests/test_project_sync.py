import importlib.util
import io
import json
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


def issue(number, node_id=None):
    return sync_project.Issue(
        number=number,
        node_id=node_id or f"I_issue_{number}",
        html_url=f"https://github.com/OrchestratorInc/agent-orchestrator/issues/{number}",
    )


def connection(nodes):
    return {"nodes": nodes, "pageInfo": {"hasNextPage": False, "endCursor": None}}


def option(option_id, name, color="BLUE", description="Reviewed route"):
    return {"id": option_id, "name": name, "color": color, "description": description}


def response(payload, status=200, headers=""):
    return unittest.mock.Mock(returncode=int(status >= 400), stderr="",
                              stdout=f"HTTP/2.0 {status}\r\n{headers}\r\n" + json.dumps(payload))


def owners():
    return {"repo": REPO, "areas": {
        "desktop-ui": {"owners": [{"login": "alice"}, {"login": "bob"}], "unowned": False},
        "comma-area": {"owners": [], "unowned": True},
        "needs-area": {"owners": [], "unowned": True},
    }}


def project(items=None, include_evidence=True, include_owner=False):
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


def project_item(item_id, number, area="", evidence="", archived=False, repo=REPO, private_title=None, owner=""):
    content = {
        "__typename": "Issue",
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
            {"node": {"isArchived": False, "fieldValueByName": None}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_new"}}},
            {"node": {"isArchived": False, "fieldValueByName": None}},
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
        human = {"node": {"isArchived": False, "fieldValueByName": {
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

    def test_apply_rate_limit_returns_partial_summary_and_cli_success(self):
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
            self.assertEqual(exit_code, 0)
            self.assertEqual(json.loads(out.getvalue())["applied"], {"fields_updated": 0, "fields_created": 0, "items_added": 1, "fields_filled": 0, "fields_skipped_after_recheck": 0})
            self.assertIn("rate limited; 1 operations applied, remaining resume on the next run", err.getvalue())
            self.assertEqual(run.call_count, 3)
            sleep.assert_called_once_with(60)

    def test_apply_rate_limit_counts_only_completed_creates_and_fills(self):
        snapshot = project([project_item("PVTI_1", 1), project_item("PVTI_2", 2)], include_owner=False)
        plan = sync_project.build_plan(mapping(), [issue(1), issue(2)], snapshot, owners=owners())
        created = sync_project.FieldInfo(OWNER_FIELD_ID, "Suggested owner", "TEXT", {})
        responses = [{"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}}, sync_project.RateLimitError("rate limited")]
        with (
            patch.object(sync_project, "_create_field", return_value=created),
            patch.object(sync_project, "_can_fill_field", return_value=True),
            patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql,
            patch.object(sync_project.sys, "stderr", io.StringIO()) as err,
        ):
            summary = sync_project.apply_plan(plan)
        self.assertEqual(summary, {"fields_updated": 0, "fields_created": 1, "items_added": 0, "fields_filled": 1, "fields_skipped_after_recheck": 0})
        self.assertEqual(graphql.call_count, 2)
        self.assertIn("rate limited; 2 operations applied", err.getvalue())

    def test_rate_limit_during_field_setup_returns_partial_counts(self):
        plan = sync_project.build_plan(mapping(), [], project(), owners=owners())
        plan.fields = {}
        plan.field_creates.insert(0, sync_project.FieldCreate("Suggested area", "SINGLE_SELECT", ("Desktop UI",)))
        created = sync_project.FieldInfo(ROUTING_FIELD_ID, "Suggested area", "SINGLE_SELECT", {})
        with patch.object(sync_project, "_create_field", side_effect=[created, sync_project.RateLimitError("rate limited")]), patch.object(sync_project.sys, "stderr", io.StringIO()) as err:
            summary = sync_project.apply_plan(plan)
        self.assertEqual(summary["fields_created"], 1)
        self.assertIn("rate limited; 1 operations applied", err.getvalue())

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

    def test_invalid_owners_stop_before_any_network_calls(self):
        args = sync_project.make_parser().parse_args(["--owners-file", "/missing/contributor-owners.json"])
        with patch.object(sync_project, "fetch_open_issues") as issues, patch.object(sync_project, "fetch_project") as snapshot, self.assertRaisesRegex(sync_project.SyncError, "cannot read owners"):
            sync_project.run(args)
        issues.assert_not_called()
        snapshot.assert_not_called()

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

    def test_owner_field_must_be_text(self):
        snapshot = project(include_owner=True)
        snapshot.fields[-1].data_type = "SINGLE_SELECT"
        with self.assertRaisesRegex(sync_project.SyncError, "Suggested owner.*TEXT"):
            sync_project.build_plan(mapping(), [issue(1)], snapshot, owners=owners())

    def test_owner_recheck_preserves_late_human_value_and_archive(self):
        snapshot = project([project_item("PVTI_1", 1, area="Desktop UI", evidence="Reviewed UI report.")], include_owner=True)
        plan = sync_project.build_plan(mapping(), [issue(1)], snapshot, owners=owners())
        for node in [
            {"isArchived": False, "fieldValueByName": {"field": {"id": OWNER_FIELD_ID, "name": "Suggested owner", "dataType": "TEXT"}, "text": "@human"}},
            {"isArchived": True, "fieldValueByName": None},
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
            {"node": {"isArchived": False, "fieldValueByName": None}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}},
        ]
        with patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
            result = sync_project.apply_plan(plan)
        self.assertEqual(result["fields_created"], 1)
        self.assertEqual(result["fields_filled"], 1)
        writes = [call for call in graphql.call_args_list if "input" in call.args[1]]
        self.assertEqual([call.args[0] for call in writes], [sync_project.CREATE_FIELD_MUTATION, sync_project.UPDATE_FIELD_MUTATION])
        self.assertEqual(writes[-1].args[1]["input"]["fieldId"], OWNER_FIELD_ID)
        self.assertEqual(writes[-1].args[1]["input"]["value"], {"text": "@alice, @bob"})

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
        self.assertEqual([spec.name for spec in plan.field_creates], ["Suggested area", "Suggested owner"])
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
            {"node": {"isArchived": False, "fieldValueByName": {"field": {"id": ROUTING_FIELD_ID, "name": "Suggested area", "dataType": "SINGLE_SELECT"}, "name": "Human choice", "optionId": "OPT_HUMAN"}}},
        ]
        with patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
            applied = sync_project.apply_plan(plan)
        self.assertEqual(applied["fields_filled"], 0)
        self.assertEqual(applied["fields_skipped_after_recheck"], 1)
        self.assertEqual(graphql.call_count, 1)

    def test_apply_skips_item_archived_after_the_plan(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project([project_item("PVTI_1", 1)]))
        with patch.object(sync_project, "_graphql_payload", return_value={"node": {"isArchived": True, "fieldValueByName": None}}):
            applied = sync_project.apply_plan(plan)
        self.assertEqual(applied["fields_filled"], 0)
        self.assertEqual(applied["fields_skipped_after_recheck"], 1)

    def test_apply_fills_only_blank_fields(self):
        plan = sync_project.build_plan(mapping(), [issue(1)], project([project_item("PVTI_1", 1)]))
        responses = [
            {"node": {"isArchived": False, "fieldValueByName": None}},
            {"updateProjectV2ItemFieldValue": {"projectV2Item": {"id": "PVTI_1"}}},
        ]
        with patch.object(sync_project, "_graphql_payload", side_effect=responses) as graphql:
            applied = sync_project.apply_plan(plan)
        self.assertEqual(applied["fields_filled"], 1)
        writes = [call.args[1]["input"] for call in graphql.call_args_list if "input" in call.args[1]]
        self.assertEqual([write["fieldId"] for write in writes], [ROUTING_FIELD_ID])

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


if __name__ == "__main__":
    unittest.main()
