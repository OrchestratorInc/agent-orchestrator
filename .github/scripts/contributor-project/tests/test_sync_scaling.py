import copy
import io
import json
import unittest
from unittest.mock import patch

from test_project_sync import sync_project as sync, mapping, owners, project, issue, project_item, REPO


class ProjectAPI:
    """Stateful API boundary, including committed writes with lost responses."""
    def __init__(self, snapshot=None):
        self.state = snapshot or project(include_owner=True)
        self.mutations = 0
        self.fail_before = None
        self.fail_after = None

    def snapshot(self):
        return copy.deepcopy(self.state)

    def __call__(self, arguments, request):
        data = json.loads(request)
        query, variables = data["query"], data["variables"]
        mutation = query.lstrip().startswith("mutation")
        if mutation:
            self.mutations += 1
            if self.mutations == self.fail_before:
                raise sync.RateLimitError("injected before commit")
        if query == sync.ADD_ITEM_MUTATION:
            n = int(variables["input"]["contentId"].rsplit("_", 1)[-1])
            item = next((i for i in self.state.items if i.issue_number == n), None)
            if item is None:
                item = sync.ItemInfo(f"item-{n}", False, "Issue", n, REPO, {}, "OPEN")
                self.state.items.append(item)
            result = {"addProjectV2ItemById": {"item": {"id": item.id}}}
        elif query == sync.FIELD_VALUE_QUERY:
            item = next(i for i in self.state.items if i.id == variables["itemId"])
            def raw(name):
                field = next(f for f in self.state.fields if f.name == name)
                value = item.values.get(field.id)
                if value is None:
                    return None
                return {"field": {"id": field.id, "name": name, "dataType": field.data_type},
                        **{("optionId" if k == "option_id" else k): v for k, v in value.items()}}
            result = {"node": {"isArchived": item.archived, "syncRecord": raw(sync.RECORD_FIELD),
                               "fieldValueByName": raw(variables["fieldName"])}}
        elif query in {sync.UPDATE_FIELD_MUTATION, sync.CLEAR_FIELD_MUTATION}:
            inp = variables["input"]
            item = next(i for i in self.state.items if i.id == inp["itemId"])
            field = next(f for f in self.state.fields if f.id == inp["fieldId"])
            value = inp.get("value")
            if value is None:
                item.values.pop(field.id, None)
            elif field.data_type == "TEXT":
                item.values[field.id] = {"text": value["text"]}
            else:
                name = next(name for name, opt in field.options.items() if opt["id"] == value["singleSelectOptionId"])
                item.values[field.id] = {"name": name, "option_id": value["singleSelectOptionId"]}
            result = {("updateProjectV2ItemFieldValue" if value is not None else "clearProjectV2ItemFieldValue"):
                      {"projectV2Item": {"id": item.id}}}
        else:
            raise AssertionError(query)
        if mutation and self.mutations == self.fail_after:
            raise sync.RateLimitError("injected after commit")
        return {"data": result}


class SyncScalingTests(unittest.TestCase):
    def apply(self, api, mapped=None, issues=None, budget=100):
        plan = sync.build_plan(mapped or mapping(), issues or [issue(1)], api.snapshot(), owners=owners())
        with patch.object(sync, "_api_request", side_effect=api), patch.object(sync.sys, "stderr", io.StringIO()):
            return sync.apply_plan(plan, max_writes=budget)

    def assert_converged(self, api, mapped=None):
        result = self.apply(api, mapped)
        self.assertTrue(result["complete"])
        pending = sync.build_plan(mapped or mapping(), [issue(1)], api.snapshot(), owners=owners())
        self.assertEqual(pending.operations, [])
        record_field = next(f for f in api.state.fields if f.name == sync.RECORD_FIELD)
        for item in api.state.items:
            record = json.loads(item.values[record_field.id]["text"])
            self.assertFalse(any(k.startswith(sync.PENDING_PREFIX) for k in record))

    def test_each_interrupted_initial_write_recovers_without_losing_ownership(self):
        for mode in ("fail_before", "fail_after"):
            for cut in range(1, 6):
                with self.subTest(mode=mode, cut=cut):
                    api = ProjectAPI()
                    setattr(api, mode, cut)
                    self.assertFalse(self.apply(api)["complete"])
                    setattr(api, mode, None)
                    self.assert_converged(api)
                    changed = mapping()
                    changed["issues"]["1"]["area"] = "comma-area"
                    self.assert_converged(api, changed)

    def test_each_interrupted_refresh_recovers_nonblank_fields(self):
        for mode in ("fail_before", "fail_after"):
            for cut in range(1, 5):
                with self.subTest(mode=mode, cut=cut):
                    api = ProjectAPI()
                    self.apply(api)
                    changed = mapping()
                    changed["issues"]["1"]["area"] = "comma-area"
                    setattr(api, mode, api.mutations + cut)
                    self.assertFalse(self.apply(api, changed)["complete"])
                    setattr(api, mode, None)
                    self.assert_converged(api, changed)

    def test_recovery_preserves_human_edits_after_interruption(self):
        api = ProjectAPI()
        api.fail_after = 3  # add, intent, area; owner has not been written
        self.assertFalse(self.apply(api)["complete"])
        api.fail_after = None
        area = next(f for f in api.state.fields if f.name == sync.ROUTING_FIELD)
        owner = next(f for f in api.state.fields if f.name == sync.OWNER_FIELD)
        api.state.items[0].values[area.id] = {"name": "Area, with comma", "option_id": "OPT_COMMA"}
        api.state.items[0].values[owner.id] = {"text": "@human"}
        self.apply(api)
        self.assertEqual(api.state.items[0].values[area.id]["name"], "Area, with comma")
        self.assertEqual(api.state.items[0].values[owner.id]["text"], "@human")
        record = next(f for f in api.state.fields if f.name == sync.RECORD_FIELD)
        self.assertFalse(any(k.startswith(sync.PENDING_PREFIX) for k in json.loads(api.state.items[0].values[record.id]["text"])))

    def test_512_issue_backfill_finishes_items_with_bounded_runs(self):
        api = ProjectAPI()
        issues = [issue(n, labels=("area:desktop-ui",)) for n in range(1, 513)]
        runs = 0
        while True:
            runs += 1
            before = api.mutations
            result = self.apply(api, issues=issues)
            self.assertLessEqual(api.mutations - before, 100)
            for item in api.state.items:
                self.assertEqual(len(item.values), 3)  # area, owner, finalized tracking record
            if result["complete"]:
                break
            self.assertEqual(result["deferred_reason"], "write_budget")
            self.assertLess(runs, 30)
        self.assertEqual(len(api.state.items), 512)
        self.assertEqual(runs, 26)
        self.assertEqual(api.mutations, 512 * 5)

    def test_capacity_skips_adds_but_still_updates_existing_items(self):
        api = ProjectAPI()
        self.apply(api)
        api.state.total_items = sync.PROJECT_CAPACITY
        mapped = mapping()
        mapped["issues"]["1"]["area"] = "comma-area"
        result = self.apply(api, mapped=mapped, issues=[issue(1), issue(2)])
        self.assertEqual(result["items_skipped_at_capacity"], 1)
        self.assertEqual(result["items_added"], 0)
        self.assertGreater(result["fields_filled"], 0)
        self.assertEqual(len(api.state.items), 1)

    def test_main_exit_codes_separate_progress_from_stalls(self):
        for applied, code in [({"complete": True, "writes": 5}, 0), ({"complete": False, "writes": 5}, 3),
                              ({"complete": False, "writes": 0}, 4),
                              ({"complete": True, "writes": 5, "items_skipped_at_capacity": 1}, 2)]:
            with self.subTest(applied=applied), patch.object(sync, "run", return_value={"applied": applied}), \
                    patch.object(sync.sys, "stdout", io.StringIO()):
                self.assertEqual(sync.main(["--project-url", "https://github.com/users/Rishet11/projects/1"]), code)

    def test_rate_limit_wait_defers_when_it_would_pass_the_run_deadline(self):
        from test_project_sync import response
        token = sync.RUN_DEADLINE.set(100)
        try:
            with patch.object(sync.subprocess, "run", return_value=response({"message": "limited"}, 429, "retry-after: 30\r\n")), \
                    patch.object(sync.time, "monotonic", return_value=90), patch.object(sync.time, "sleep") as sleep:
                with self.assertRaisesRegex(sync.RateLimitError, "deadline"):
                    sync._graphql_payload("query { viewer { login } }", {})
            sleep.assert_not_called()
        finally:
            sync.RUN_DEADLINE.reset(token)

    def test_write_pacing_uses_monotonic_time(self):
        budget = sync.WriteBudget(10, interval=1)
        with patch.object(sync.time, "monotonic", side_effect=[5, 5.25, 6]), patch.object(sync.time, "sleep") as sleep:
            budget.consume()
            budget.consume()
        sleep.assert_called_once_with(.75)

    def test_primary_reset_defers_without_sleeping_past_the_job_budget(self):
        from test_project_sync import response
        with patch.object(sync.subprocess, "run", return_value=response({"message": "limited"}, 403,
                "x-ratelimit-remaining: 0\r\nx-ratelimit-reset: 2000\r\n")), \
                patch.object(sync.time, "time", return_value=1000), patch.object(sync.time, "sleep") as sleep:
            with self.assertRaisesRegex(sync.RateLimitError, "defer"):
                sync._graphql_payload("query { viewer { login } }", {})
        sleep.assert_not_called()

    def test_mutation_retries_also_consume_the_write_budget(self):
        from test_project_sync import response
        budget = sync.WriteBudget(10)
        token = sync.WRITE_BUDGET.set(budget)
        try:
            with patch.object(sync.subprocess, "run", side_effect=[response({"message": "busy"}, 503), response({"data": {"ok": True}})]), \
                    patch.object(sync.time, "sleep"), patch.object(sync.sys, "stderr", io.StringIO()):
                self.assertEqual(sync._graphql_payload("mutation { test }", {}), {"ok": True})
            self.assertEqual(budget.used, 2)
        finally:
            sync.WRITE_BUDGET.reset(token)


class ArchiveScanTests(unittest.TestCase):
    def test_reads_active_items_and_only_open_archives_with_global_capacity_count(self):
        from test_project_sync import connection
        fields = project(include_owner=True).fields
        raw_fields = [{"id": f.id, "name": f.name, "dataType": f.data_type,
                       "options": list(f.options.values())} for f in fields]
        calls = []
        def graphql(query, variables):
            if "OWNER_PROJECT" in query:
                return {"OWNER_PROJECT": {"projectV2": {"id": "project", "url": "https://github.com/orgs/OrchestratorInc/projects/7",
                    "itemCount": {"totalCount": 45000}, "fields": connection(raw_fields)}}}
            calls.append(variables)
            if variables["archivedStates"] == ["NOT_ARCHIVED"]:
                items = [project_item("active", 1), project_item("closed-active", 2, state="CLOSED")]
            else:
                self.assertEqual(variables["filter"], "is:open")
                items = [project_item("human-archive", 3, archived=True)]
            return {"node": {"items": connection(items)}}
        with patch.object(sync, "_graphql_payload", side_effect=graphql), patch.object(sync.sys, "stderr", io.StringIO()) as err:
            snapshot = sync.fetch_project(sync.parse_project_url("https://github.com/orgs/OrchestratorInc/projects/7"))
        self.assertEqual([i.id for i in snapshot.items], ["active", "closed-active", "human-archive"])
        self.assertEqual(snapshot.total_items, 45000)
        self.assertEqual(len(calls), 2)
        self.assertIn("Rotate to a new private Project", err.getvalue())
        plan = sync.build_plan(mapping(), [issue(1), issue(3)], snapshot)
        self.assertEqual(plan.total_items, 45000)
        self.assertFalse(any(op.kind == "add" and op.issue.number == 3 for op in plan.operations))
