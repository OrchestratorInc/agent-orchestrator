#!/usr/bin/env python3
"""Plan and optionally apply safe routing updates to a GitHub Project."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
import time
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from dataclasses import dataclass
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
ROUTING_FIELD = "Suggested area"
OWNER_FIELD = "Suggested owner"
PROJECT_URL_RE = re.compile(
    r"^https://github\.com/(?P<owner_kind>orgs|users)/(?P<owner>[^/]+)/projects/(?P<number>[1-9][0-9]*)/?$"
)


class SyncError(RuntimeError):
    """A validation, fixture, or GitHub API error that must stop the sync."""


class RateLimitError(SyncError):
    """GitHub is still rate limited after the one allowed retry."""


TRANSIENT_RETRY_SECONDS = 5.0
GITHUB_INTERNAL_ERROR = "Something went wrong while executing your query"


@dataclass(frozen=True)
class ProjectRef:
    url: str
    owner_kind: str
    owner: str
    number: int


@dataclass
class FieldInfo:
    id: str
    name: str
    data_type: str
    options: dict[str, dict[str, str]]


@dataclass
class ItemInfo:
    id: str
    archived: bool
    content_type: str | None
    issue_number: int | None
    repository: str | None
    values: dict[str, dict[str, Any]]


@dataclass
class ProjectSnapshot:
    id: str
    url: str
    fields: list[FieldInfo]
    items: list[ItemInfo]


@dataclass(frozen=True)
class Issue:
    number: int
    node_id: str
    html_url: str


@dataclass(frozen=True)
class FieldCreate:
    name: str
    data_type: str
    options: tuple[str, ...] = ()


@dataclass(frozen=True)
class Operation:
    kind: str
    issue: Issue
    field_name: str | None = None
    field_id: str | None = None
    value: str | None = None
    item_id: str | None = None


@dataclass
class SyncPlan:
    repo: str
    project_id: str
    project_url: str
    field_creates: list[FieldCreate]
    field_updates: dict[str, tuple[str, ...]]
    operations: list[Operation]
    source_issue_count: int
    project_item_count: int
    source_item_count: int
    archived_source_item_count: int
    preserved_item_count: int
    fields: dict[str, FieldInfo]


def _nonempty(value: Any) -> bool:
    return isinstance(value, str) and bool(value.strip())


def parse_project_url(url: str) -> ProjectRef:
    match = PROJECT_URL_RE.fullmatch(url.strip())
    if not match:
        raise SyncError(
            "project URL must be https://github.com/orgs/OWNER/projects/N "
            "or https://github.com/users/OWNER/projects/N"
        )
    return ProjectRef(
        url=url.rstrip("/"),
        owner_kind=match.group("owner_kind"),
        owner=match.group("owner"),
        number=int(match.group("number")),
    )


def load_mapping(path: Path) -> dict[str, Any]:
    try:
        mapping = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise SyncError(f"cannot read mapping file {path}: {exc}") from exc
    if not isinstance(mapping, dict):
        raise SyncError("mapping must be a JSON object")
    repo = mapping.get("repo")
    if not isinstance(repo, str) or repo.count("/") != 1 or not all(repo.split("/")):
        raise SyncError("mapping.repo must be OWNER/REPOSITORY")
    areas = mapping.get("areas")
    if not isinstance(areas, list) or not areas:
        raise SyncError("mapping.areas must be a non-empty array")
    area_ids: set[str] = set()
    area_labels: set[str] = set()
    for area in areas:
        if not isinstance(area, dict) or not isinstance(area.get("id"), str) or not _nonempty(area.get("label")):
            raise SyncError("every mapping area needs a nonempty id and label")
        if area["id"] in area_ids or area["label"] in area_labels:
            raise SyncError("mapping area ids and labels must be unique")
        area_ids.add(area["id"])
        area_labels.add(area["label"])
    if "needs-area" not in area_ids:
        raise SyncError("mapping must include the needs-area bucket")
    issue_map = mapping.get("issues", {})
    if not isinstance(issue_map, dict):
        raise SyncError("mapping.issues must be an object")
    for number, entry in issue_map.items():
        if not str(number).isdigit() or not isinstance(entry, dict):
            raise SyncError("mapping issue keys must be numeric and map to objects")
        area_id = entry.get("area")
        if not isinstance(area_id, str) or area_id not in area_ids:
            raise SyncError(f"mapping issue {number} references an unknown area")
    return mapping


def load_owners(path: Path, mapping: dict[str, Any]) -> dict[str, Any]:
    owners = _load_json_file(path, "owners")
    if not isinstance(owners, dict) or owners.get("repo") != mapping["repo"]:
        raise SyncError("owners.repo must match mapping.repo")
    areas = owners.get("areas")
    if not isinstance(areas, dict) or set(areas) != {area["id"] for area in mapping["areas"]}:
        raise SyncError("owners.areas must match the mapping area ids")
    for area_id, area in areas.items():
        if not isinstance(area, dict) or not isinstance(area.get("owners"), list) or len(area["owners"]) > 3:
            raise SyncError(f"owners area {area_id} must contain up to three owners")
        logins = [item.get("login") if isinstance(item, dict) else None for item in area["owners"]]
        if any(not isinstance(login, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]{0,38}", login) for login in logins):
            raise SyncError(f"owners area {area_id} has an invalid login")
        if len({login.lower() for login in logins}) != len(logins):
            raise SyncError(f"owners area {area_id} has duplicate logins")
        if not isinstance(area.get("unowned"), bool) or area["unowned"] != (not logins):
            raise SyncError(f"owners area {area_id} has an inconsistent unowned flag")
    return owners


def normalize_open_issues(raw_pages: Any, repo: str) -> list[Issue]:
    """Keep only open issues, excluding REST's pull-request-shaped entries."""
    if isinstance(raw_pages, list) and (not raw_pages or all(isinstance(entry, dict) for entry in raw_pages)):
        pages = [raw_pages]
    else:
        pages = raw_pages if isinstance(raw_pages, list) else [raw_pages]
    raw_issues: list[Any] = []
    for page in pages:
        if not isinstance(page, list):
            raise SyncError("issues response must contain REST page arrays")
        raw_issues.extend(page)

    issues: list[Issue] = []
    seen: set[int] = set()
    for raw in raw_issues:
        if not isinstance(raw, dict) or "pull_request" in raw:
            continue
        if raw.get("state", "open") != "open":
            continue
        try:
            number = int(raw["number"])
            node_id = str(raw["node_id"])
            html_url = str(raw["html_url"])
        except (KeyError, TypeError, ValueError) as exc:
            raise SyncError("every open issue needs number, node_id, and html_url") from exc
        if number <= 0 or not _nonempty(node_id) or html_url != f"https://github.com/{repo}/issues/{number}":
            raise SyncError(f"invalid open issue metadata for #{number}")
        if number in seen:
            raise SyncError(f"duplicate open issue #{number} in paginated response")
        seen.add(number)
        issues.append(Issue(number=number, node_id=node_id, html_url=html_url))
    return issues


def load_issues_file(path: Path, repo: str) -> list[Issue]:
    try:
        raw = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise SyncError(f"cannot read issues file {path}: {exc}") from exc
    return normalize_open_issues(raw, repo)


def _normalize_field(raw: Any) -> FieldInfo:
    if not isinstance(raw, dict) or not all(_nonempty(raw.get(key)) for key in ("id", "name", "dataType")):
        raise SyncError("project fields need id, name, and dataType")
    options: dict[str, dict[str, str]] = {}
    for option in raw.get("options", []):
        if not isinstance(option, dict) or not all(_nonempty(option.get(key)) for key in ("id", "name", "color")) or not isinstance(option.get("description"), str):
            raise SyncError(f"project field {raw['name']!r} has an incomplete option")
        if option["name"] in options:
            raise SyncError(f"project field {raw['name']!r} has duplicate option {option['name']!r}")
        options[option["name"]] = {key: option[key] for key in ("id", "name", "color", "description")}
    return FieldInfo(id=raw["id"], name=raw["name"], data_type=raw["dataType"], options=options)


def _normalize_item_value(raw: Any) -> tuple[str, dict[str, Any]] | None:
    if not isinstance(raw, dict) or not isinstance(raw.get("field"), dict) or not raw["field"].get("id"):
        return None
    return raw["field"]["id"], {"text": raw.get("text"), "name": raw.get("name"), "option_id": raw.get("optionId")}


def _normalize_item(raw: Any) -> ItemInfo:
    if not isinstance(raw, dict) or not _nonempty(raw.get("id")) or not isinstance(raw.get("isArchived"), bool):
        raise SyncError("project items need id and isArchived")
    content = raw.get("content") or {}
    repository = content.get("repository") or {}
    connection = raw.get("fieldValues")
    if not isinstance(connection, dict) or not isinstance(connection.get("nodes"), list):
        raise SyncError("project item needs a fieldValues nodes connection")
    values = {}
    for value in connection["nodes"]:
        normalized = _normalize_item_value(value)
        if normalized:
            values[normalized[0]] = normalized[1]
    return ItemInfo(
        id=raw["id"], archived=raw["isArchived"], content_type=content.get("__typename"),
        issue_number=content.get("number"), repository=repository.get("nameWithOwner"), values=values,
    )


def normalize_project_snapshot(raw: Any, fallback_url: str | None = None) -> ProjectSnapshot:
    """Read a GraphQL ProjectV2 object, including its fields/items connections."""
    if not isinstance(raw, dict) or not _nonempty(raw.get("id")):
        raise SyncError("project snapshot needs a project id")
    project_url = raw.get("url") or fallback_url
    if not project_url:
        raise SyncError("project snapshot needs a project URL or --project-url")
    fields_raw = raw.get("fields")
    items_raw = raw.get("items")
    if not isinstance(fields_raw, dict) or not isinstance(fields_raw.get("nodes"), list) or not isinstance(items_raw, dict) or not isinstance(items_raw.get("nodes"), list):
        raise SyncError("project snapshot needs fields and items nodes connections")
    fields = [_normalize_field(field) for field in fields_raw["nodes"]]
    if len({field.name for field in fields}) != len(fields):
        raise SyncError("project contains duplicate field names")
    return ProjectSnapshot(id=raw["id"], url=project_url, fields=fields,
                           items=[_normalize_item(item) for item in items_raw["nodes"]])


def _has_nonempty_value(item: ItemInfo, field: FieldInfo) -> bool:
    value = item.values.get(field.id)
    if not value:
        return False
    if field.data_type == "SINGLE_SELECT":
        return _nonempty(value.get("name")) or _nonempty(value.get("option_id"))
    return _nonempty(value.get("text"))


def _field_map(snapshot: ProjectSnapshot, areas: list[dict[str, str]], include_owners: bool = False) -> tuple[dict[str, FieldInfo], list[FieldCreate], dict[str, tuple[str, ...]]]:
    fields = {field.name: field for field in snapshot.fields}
    required_labels = tuple(area["label"] for area in areas)
    creates: list[FieldCreate] = []
    updates: dict[str, tuple[str, ...]] = {}

    routing = fields.get(ROUTING_FIELD)
    if routing is None:
        creates.append(FieldCreate(ROUTING_FIELD, "SINGLE_SELECT", required_labels))
    else:
        if routing.data_type != "SINGLE_SELECT":
            raise SyncError(f"managed field {ROUTING_FIELD!r} must be SINGLE_SELECT, found {routing.data_type}")
        missing = tuple(label for label in required_labels if label not in routing.options)
        if missing:
            updates[ROUTING_FIELD] = missing

    owner = fields.get(OWNER_FIELD)
    if owner is not None and owner.data_type != "TEXT":
        raise SyncError(f"managed field {OWNER_FIELD!r} must be TEXT, found {owner.data_type}")
    if owner is None and include_owners:
        creates.append(FieldCreate(OWNER_FIELD, "TEXT"))
    return fields, creates, updates


def build_plan(mapping: dict[str, Any], issues: list[Issue], snapshot: ProjectSnapshot, owners: dict[str, Any] | None = None) -> SyncPlan:
    """Build an idempotent plan without making any GitHub mutation."""
    fields, field_creates, field_updates = _field_map(snapshot, mapping["areas"], owners is not None)
    routing = fields.get(ROUTING_FIELD)
    owner = fields.get(OWNER_FIELD)
    source_items: dict[int, list[ItemInfo]] = {}
    archived_source_numbers: set[int] = set()
    archived_source_count = 0
    preserved_count = 0
    for item in snapshot.items:
        if item.repository != mapping["repo"] or item.content_type != "Issue" or item.issue_number is None:
            preserved_count += 1
            continue
        if item.archived:
            archived_source_count += 1
            if item.issue_number is not None:
                archived_source_numbers.add(item.issue_number)
            continue
        source_items.setdefault(item.issue_number, []).append(item)

    operations: list[Operation] = []

    def add_fills(issue: Issue, item_id: str | None, item_values: ItemInfo | None) -> None:
        area_id = mapping.get("issues", {}).get(str(issue.number), {}).get("area", "needs-area")
        area_label = next(area["label"] for area in mapping["areas"] if area["id"] == area_id)
        if routing is None or item_values is None or not _has_nonempty_value(item_values, routing):
            operations.append(
                Operation(
                    kind="fill",
                    issue=issue,
                    field_name=ROUTING_FIELD,
                    field_id=routing.id if routing else None,
                    value=area_label,
                    item_id=item_id,
                )
            )
        if owners is not None and area_id not in {"shared-infra", "needs-area"}:
            area_owners = owners["areas"][area_id]["owners"]
            owner_text = ", ".join("@" + person["login"] for person in area_owners)
            if owner_text and (owner is None or item_values is None or not _has_nonempty_value(item_values, owner)):
                operations.append(Operation(kind="fill", issue=issue, field_name=OWNER_FIELD,
                                            field_id=owner.id if owner else None, value=owner_text, item_id=item_id))

    for issue in issues:
        existing = source_items.get(issue.number, [])
        if not existing:
            if issue.number in archived_source_numbers:
                continue
            operations.append(Operation(kind="add", issue=issue))
            add_fills(issue, None, None)
            continue
        for item in existing:
            add_fills(issue, item.id, item)

    return SyncPlan(
        repo=mapping["repo"],
        project_id=snapshot.id,
        project_url=snapshot.url,
        field_creates=field_creates,
        field_updates=field_updates,
        operations=operations,
        source_issue_count=len(issues),
        project_item_count=len(snapshot.items),
        source_item_count=sum(len(items) for items in source_items.values()),
        archived_source_item_count=archived_source_count,
        preserved_item_count=preserved_count,
        fields=fields,
    )


def _retry_transient(reason: str) -> None:
    print(f"transient GitHub API failure, retrying once in {TRANSIENT_RETRY_SECONDS:g}s: {reason}", file=sys.stderr)
    time.sleep(TRANSIENT_RETRY_SECONDS)


def _api_request(arguments: list[str], request: str | None = None) -> Any:
    # Rate limits and transient failures each get one retry of their own.
    rate_limit_retried = transient_retried = False
    while True:
        try:
            result = subprocess.run(["gh", "api", "--include", *arguments], input=request,
                                    capture_output=True, text=True, timeout=150, check=False)
        except subprocess.TimeoutExpired as exc:
            if transient_retried:
                raise SyncError("gh API request timed out after one retry") from exc
            transient_retried = True
            _retry_transient("request timed out")
            continue
        except OSError as exc:
            raise SyncError(f"cannot run gh: {exc}") from exc
        headers, separator, body = result.stdout.replace("\r\n", "\n").partition("\n\n")
        status = re.match(r"HTTP/\S+ (\d{3})", headers)
        if not status or not separator:
            # gh prints no response when the connection itself failed.
            if transient_retried:
                raise SyncError(f"gh API response has no HTTP headers: {result.stderr.strip()}")
            transient_retried = True
            _retry_transient(result.stderr.strip() or "no HTTP response")
            continue
        retry = re.search(r"(?im)^retry-after:[ \t]*(.*)$", headers)
        http_rate_limited = int(status[1]) == 429 or (int(status[1]) == 403 and (
            retry is not None or re.search(r"(?im)^x-ratelimit-remaining:[ \t]*0[ \t]*$", headers)
            or re.search(r"secondary[ -]rate[ -]limit", body, re.IGNORECASE)
        ))
        server_error = int(status[1]) >= 500
        try:
            payload = json.loads(body)
        except json.JSONDecodeError as exc:
            if not (http_rate_limited or server_error):
                raise SyncError(f"gh API returned invalid JSON: {result.stderr.strip()}") from exc
            payload = {}
        server_error = server_error or (isinstance(payload, dict) and any(
            isinstance(error, dict) and str(error.get("message", "")).startswith(GITHUB_INTERNAL_ERROR)
            for error in (payload.get("errors") or [])
        ))
        if server_error and not transient_retried:
            transient_retried = True
            _retry_transient(f"HTTP {status[1]} {body.strip()[:200]}")
            continue
        graphql_rate_limited = isinstance(payload, dict) and any(
            isinstance(error, dict) and error.get("type") == "RATE_LIMITED"
            for error in (payload.get("errors") or [])
        )
        if http_rate_limited or graphql_rate_limited:
            if rate_limit_retried:
                raise RateLimitError(f"GitHub API rate limited (HTTP {status[1]})")
            delay = 60.0
            if retry:
                try:
                    delay = float(retry[1])
                except ValueError:
                    try:
                        delay = (parsedate_to_datetime(retry[1]) - datetime.now(timezone.utc)).total_seconds()
                    except (TypeError, ValueError):
                        pass
            rate_limit_retried = True
            time.sleep(max(0, delay))
            continue
        if isinstance(payload, dict) and payload.get("errors"):
            messages = [error.get("message", "Unknown GitHub error") for error in payload["errors"] if isinstance(error, dict)]
            raise SyncError("GitHub GraphQL: " + "; ".join(messages))
        if result.returncode or int(status[1]) >= 400:
            message = payload.get("message", "") if isinstance(payload, dict) else ""
            raise SyncError(f"gh API request failed (HTTP {status[1]}): {message} {result.stderr.strip()}")
        return payload


def _graphql_payload(query: str, variables: dict[str, Any]) -> dict[str, Any]:
    request = json.dumps({"query": query, "variables": variables}, ensure_ascii=False)
    payload = _api_request(["graphql", "--input", "-"], request)
    data = payload.get("data") if isinstance(payload, dict) else None
    if not isinstance(data, dict):
        raise SyncError("GitHub GraphQL response did not contain data")
    return data


def _rest_pages(endpoint: str) -> list[list[dict[str, Any]]]:
    pages = []
    while True:
        page = _api_request([f"{endpoint}&page={len(pages) + 1}"])
        if not isinstance(page, list):
            raise SyncError("issues response must contain REST page arrays")
        pages.append(page)
        if len(page) < 100:
            return pages


def fetch_open_issues(repo: str) -> list[Issue]:
    raw_pages = _rest_pages(f"repos/{repo}/issues?state=open&per_page=100")
    return normalize_open_issues(raw_pages, repo)


PROJECT_FIELDS_QUERY = """
query($owner: String!, $number: Int!, $after: String) {
  OWNER_PROJECT: %s(login: $owner) {
    projectV2(number: $number) {
      id
      url
      fields(first: 100, after: $after) {
        nodes {
          ... on ProjectV2FieldCommon { id name dataType }
          ... on ProjectV2SingleSelectField { options { id name color description } }
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}
"""

PROJECT_ITEMS_QUERY = """
query($projectId: ID!, $after: String) {
  node(id: $projectId) {
    ... on ProjectV2 {
      items(first: 100, after: $after) {
        nodes {
          id
          isArchived
          content {
            __typename
            ... on Issue { number repository { nameWithOwner } }
            ... on PullRequest { number repository { nameWithOwner } }
            ... on DraftIssue { id }
          }
          fieldValues(first: 100) {
            nodes {
              ... on ProjectV2ItemFieldSingleSelectValue {
                field { ... on ProjectV2FieldCommon { id name } }
                optionId
                name
              }
              ... on ProjectV2ItemFieldTextValue {
                field { ... on ProjectV2FieldCommon { id name } }
                text
              }
            }
            pageInfo { hasNextPage endCursor }
          }
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}
"""

ITEM_FIELD_VALUES_QUERY = """
query($itemId: ID!, $after: String) {
  node(id: $itemId) {
    ... on ProjectV2Item {
      fieldValues(first: 100, after: $after) {
        nodes {
          ... on ProjectV2ItemFieldSingleSelectValue {
            field { ... on ProjectV2FieldCommon { id name } }
            optionId
            name
          }
          ... on ProjectV2ItemFieldTextValue {
            field { ... on ProjectV2FieldCommon { id name } }
            text
          }
        }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}
"""


FIELD_VALUE_QUERY = """
query($itemId: ID!, $fieldName: String!) {
  node(id: $itemId) {
    ... on ProjectV2Item {
      isArchived
      fieldValueByName(name: $fieldName) {
        ... on ProjectV2ItemFieldSingleSelectValue {
          field { ... on ProjectV2FieldCommon { id name dataType } }
          optionId
          name
        }
        ... on ProjectV2ItemFieldTextValue {
          field { ... on ProjectV2FieldCommon { id name dataType } }
          text
        }
      }
    }
  }
}
"""


def _can_fill_field(item_id: str, field: FieldInfo) -> bool:
    data = _graphql_payload(FIELD_VALUE_QUERY, {"itemId": item_id, "fieldName": field.name})
    node = data.get("node")
    if not isinstance(node, dict) or not isinstance(node.get("isArchived"), bool) or "fieldValueByName" not in node:
        raise SyncError("GitHub did not return the current item field and archive state")
    if node["isArchived"]:
        return False
    value = node["fieldValueByName"]
    if value is None:
        return True
    if not isinstance(value, dict) or not isinstance(value.get("field"), dict):
        raise SyncError("GitHub returned an incomplete current field value")
    current_field = value["field"]
    if current_field.get("id") != field.id or current_field.get("dataType") != field.data_type:
        raise SyncError(f"managed field {field.name!r} changed while the sync was running")
    item = _normalize_item({"id": item_id, "isArchived": False, "fieldValues": {"nodes": [value]}})
    return not _has_nonempty_value(item, field)


def fetch_project(ref: ProjectRef) -> ProjectSnapshot:
    owner_field = "organization" if ref.owner_kind == "orgs" else "user"
    fields: list[dict[str, Any]] = []
    project_id = ""
    project_url = ref.url
    cursor: str | None = None
    while True:
        data = _graphql_payload(PROJECT_FIELDS_QUERY % owner_field, {"owner": ref.owner, "number": ref.number, "after": cursor})
        owner = data.get("OWNER_PROJECT")
        project = owner.get("projectV2") if isinstance(owner, dict) else None
        if not isinstance(project, dict):
            raise SyncError("GitHub did not return the requested project")
        project_id = str(project.get("id") or "")
        project_url = str(project.get("url") or project_url)
        connection = project.get("fields")
        if not isinstance(connection, dict) or not isinstance(connection.get("nodes"), list):
            raise SyncError("GitHub returned an incomplete project fields connection")
        fields.extend(connection["nodes"])
        page_info = connection.get("pageInfo")
        if not isinstance(page_info, dict):
            raise SyncError("GitHub returned an incomplete project fields page")
        if not page_info.get("hasNextPage"):
            break
        cursor = page_info.get("endCursor")
        if not cursor:
            raise SyncError("GitHub project fields pagination did not provide a cursor")

    items: list[dict[str, Any]] = []
    cursor = None
    while True:
        data = _graphql_payload(PROJECT_ITEMS_QUERY, {"projectId": project_id, "after": cursor})
        node = data.get("node")
        connection = node.get("items") if isinstance(node, dict) else None
        if not isinstance(connection, dict) or not isinstance(connection.get("nodes"), list):
            raise SyncError("GitHub returned an incomplete project items connection")
        for item in connection["nodes"]:
            if not isinstance(item, dict):
                raise SyncError("GitHub returned an incomplete project item")
            item_values = item.get("fieldValues")
            if not isinstance(item_values, dict) or not isinstance(item_values.get("nodes"), list) or not isinstance(item_values.get("pageInfo"), dict):
                raise SyncError("GitHub returned an incomplete project field-values connection")
            if item_values.get("pageInfo", {}).get("hasNextPage"):
                item_cursor = item_values.get("pageInfo", {}).get("endCursor")
                if not item_cursor:
                    raise SyncError("GitHub project field-values pagination did not provide a cursor")
                extra_values: list[dict[str, Any]] = []
                while item_cursor:
                    value_data = _graphql_payload(ITEM_FIELD_VALUES_QUERY, {"itemId": item["id"], "after": item_cursor})
                    value_node = value_data.get("node")
                    value_connection = value_node.get("fieldValues") if isinstance(value_node, dict) else None
                    if not isinstance(value_connection, dict) or not isinstance(value_connection.get("nodes"), list):
                        raise SyncError("GitHub returned an incomplete paginated field-values connection")
                    extra_values.extend(value_connection["nodes"])
                    value_page = value_connection.get("pageInfo")
                    if not isinstance(value_page, dict):
                        raise SyncError("GitHub returned an incomplete paginated field-values page")
                    if not value_page.get("hasNextPage"):
                        item_cursor = None
                    else:
                        item_cursor = value_page.get("endCursor")
                        if not item_cursor:
                            raise SyncError("GitHub project field-values pagination did not provide a cursor")
                item["fieldValues"]["nodes"] = item_values.get("nodes", []) + extra_values
            items.append(item)
        page_info = connection.get("pageInfo")
        if not isinstance(page_info, dict):
            raise SyncError("GitHub returned an incomplete project items page")
        if not page_info.get("hasNextPage"):
            break
        cursor = page_info.get("endCursor")
        if not cursor:
            raise SyncError("GitHub project items pagination did not provide a cursor")

    return normalize_project_snapshot({"id": project_id, "url": project_url, "fields": {"nodes": fields}, "items": {"nodes": items}}, ref.url)


CREATE_FIELD_MUTATION = """
mutation($input: CreateProjectV2FieldInput!) {
  createProjectV2Field(input: $input) {
    projectV2Field {
      ... on ProjectV2FieldCommon { id name dataType }
      ... on ProjectV2SingleSelectField { options { id name color description } }
    }
  }
}
"""

FIELD_OPTIONS_QUERY = """
query($fieldId: ID!) {
  node(id: $fieldId) {
    ... on ProjectV2SingleSelectField { id name dataType options { id name color description } }
  }
}
"""

UPDATE_OPTIONS_MUTATION = """
mutation($input: UpdateProjectV2FieldInput!) {
  updateProjectV2Field(input: $input) {
    projectV2Field {
      ... on ProjectV2SingleSelectField { id name dataType options { id name color description } }
    }
  }
}
"""

ADD_ITEM_MUTATION = """
mutation($input: AddProjectV2ItemByIdInput!) {
  addProjectV2ItemById(input: $input) { item { id } }
}
"""

UPDATE_FIELD_MUTATION = """
mutation($input: UpdateProjectV2ItemFieldValueInput!) {
  updateProjectV2ItemFieldValue(input: $input) { projectV2Item { id } }
}
"""


def _add_options(field: FieldInfo, labels: tuple[str, ...]) -> FieldInfo:
    data = _graphql_payload(FIELD_OPTIONS_QUERY, {"fieldId": field.id})
    current = _normalize_field(data.get("node"))
    if (current.id, current.name, current.data_type) != (field.id, field.name, "SINGLE_SELECT"):
        raise SyncError(f"managed field {field.name!r} changed while the sync was running")
    missing = [label for label in labels if label not in current.options]
    if not missing:
        return current
    # GitHub replaces the full option list. IDs preserve existing item selections.
    options = list(current.options.values()) + [
        {"name": label, "color": "GRAY", "description": "Contributor routing option"} for label in missing
    ]
    data = _graphql_payload(UPDATE_OPTIONS_MUTATION, {"input": {"fieldId": field.id, "singleSelectOptions": options}})
    payload = data.get("updateProjectV2Field")
    updated = _normalize_field(payload.get("projectV2Field") if isinstance(payload, dict) else None)
    if (updated.id, updated.name, updated.data_type) != (current.id, current.name, current.data_type):
        raise SyncError(f"GitHub returned the wrong field after updating {field.name!r}")
    if any(updated.options.get(name) != option for name, option in current.options.items()):
        raise SyncError(f"GitHub changed an existing option in {field.name!r}")
    if any(label not in updated.options for label in labels):
        raise SyncError(f"GitHub did not add all required options to {field.name!r}")
    return updated


def _create_field(project_id: str, spec: FieldCreate) -> FieldInfo:
    input_data: dict[str, Any] = {"projectId": project_id, "name": spec.name, "dataType": spec.data_type}
    if spec.data_type == "SINGLE_SELECT":
        colors = ("BLUE", "GRAY", "GREEN", "ORANGE", "PINK", "PURPLE", "RED", "YELLOW")
        input_data["singleSelectOptions"] = [
            {
                "name": label,
                "description": "Contributor routing option",
                "color": colors[index % len(colors)],
            }
            for index, label in enumerate(spec.options)
        ]
    data = _graphql_payload(CREATE_FIELD_MUTATION, {"input": input_data})
    payload = data.get("createProjectV2Field")
    field = payload.get("projectV2Field") if isinstance(payload, dict) else None
    if not isinstance(field, dict):
        raise SyncError(f"GitHub did not return the created field {spec.name!r}")
    created = _normalize_field(field)
    if created.name != spec.name or created.data_type != spec.data_type:
        raise SyncError(f"GitHub returned the wrong type for created field {spec.name!r}")
    missing = [label for label in spec.options if label not in created.options]
    if missing:
        raise SyncError(f"created field {spec.name!r} is missing required options")
    return created


def apply_plan(plan: SyncPlan) -> dict[str, int]:
    """Apply only operations already validated and present in ``plan``."""
    summary = {"fields_updated": 0, "fields_created": 0, "items_added": 0,
               "fields_filled": 0, "fields_skipped_after_recheck": 0}
    try:
        fields = dict(plan.fields)
        for name, labels in plan.field_updates.items():
            fields[name] = _add_options(fields[name], labels)
            summary["fields_updated"] += 1
        for spec in plan.field_creates:
            if spec.name in fields:
                raise SyncError(f"refusing to create already-present managed field {spec.name!r}")
            fields[spec.name] = _create_field(plan.project_id, spec)
            summary["fields_created"] += 1

        routing = fields.get(ROUTING_FIELD)
        if routing is None:
            raise SyncError(f"managed field {ROUTING_FIELD!r} was not available after field creation")
        added: dict[int, str] = {}
        for operation in plan.operations:
            if operation.kind != "add":
                continue
            data = _graphql_payload(
                ADD_ITEM_MUTATION,
                {"input": {"projectId": plan.project_id, "contentId": operation.issue.node_id}},
            )
            payload = data.get("addProjectV2ItemById")
            item = payload.get("item") if isinstance(payload, dict) else None
            item_id = item.get("id") if isinstance(item, dict) else None
            if not item_id:
                raise SyncError(f"GitHub did not return a project item for issue #{operation.issue.number}")
            added[operation.issue.number] = str(item_id)
            summary["items_added"] += 1

        for operation in plan.operations:
            if operation.kind != "fill" or not operation.value:
                continue
            field = fields.get(operation.field_name or "")
            if field is None:
                raise SyncError(f"managed field {operation.field_name!r} was not available after field creation")
            item_id = operation.item_id or added.get(operation.issue.number)
            if not item_id:
                raise SyncError(f"cannot fill issue #{operation.issue.number} without a project item id")
            # Recheck added items too: addProjectV2ItemById returns the existing item when the
            # Project item list lagged, so a "new" item may already hold a human value.
            if not _can_fill_field(item_id, field):
                summary["fields_skipped_after_recheck"] += 1
                continue
            if field.data_type == "SINGLE_SELECT":
                option_id = field.options.get(operation.value, {}).get("id")
                if not option_id:
                    raise SyncError(f"managed field {field.name!r} has no option for {operation.value!r}")
                value: dict[str, Any] = {"singleSelectOptionId": option_id}
            elif field.data_type == "TEXT":
                value = {"text": operation.value}
            else:
                raise SyncError(f"cannot update managed field {field.name!r} of type {field.data_type}")
            data = _graphql_payload(
                UPDATE_FIELD_MUTATION,
                {
                    "input": {
                        "projectId": plan.project_id,
                        "itemId": item_id,
                        "fieldId": field.id,
                        "value": value,
                    }
                },
            )
            payload = data.get("updateProjectV2ItemFieldValue")
            updated_item = payload.get("projectV2Item") if isinstance(payload, dict) else None
            if not isinstance(updated_item, dict) or not updated_item.get("id"):
                raise SyncError(f"GitHub did not confirm the field update for issue #{operation.issue.number}")
            summary["fields_filled"] += 1
    except RateLimitError:
        applied_count = sum(summary[key] for key in ("fields_updated", "fields_created", "items_added", "fields_filled"))
        print(f"rate limited; {applied_count} operations applied, remaining resume on the next run", file=sys.stderr)
    return summary


def plan_to_json(plan: SyncPlan, mode: str, applied: dict[str, int] | None = None) -> dict[str, Any]:
    operations = []
    for operation in plan.operations:
        row: dict[str, Any] = {
            "operation": operation.kind,
            "issue_number": operation.issue.number,
            "issue_node_id": operation.issue.node_id,
            "url": operation.issue.html_url,
        }
        if operation.field_name:
            row["field"] = operation.field_name
            row["value"] = operation.value
        if operation.item_id:
            row["project_item_id"] = operation.item_id
        operations.append(row)
    output: dict[str, Any] = {
        "mode": mode,
        "repo": plan.repo,
        "project_id": plan.project_id,
        "project_url": plan.project_url,
        "counts": {
            "source_open_issues": plan.source_issue_count,
            "project_items": plan.project_item_count,
            "source_items": plan.source_item_count,
            "archived_source_items_skipped": plan.archived_source_item_count,
            "unrelated_or_private_items_preserved": plan.preserved_item_count,
            "items_to_add": sum(operation.kind == "add" for operation in plan.operations),
            "fields_to_fill": sum(operation.kind == "fill" for operation in plan.operations),
            "fields_to_create": len(plan.field_creates),
            "fields_to_update": len(plan.field_updates),
        },
        "field_creates": [
            {"name": spec.name, "data_type": spec.data_type, "options": list(spec.options)}
            for spec in plan.field_creates
        ],
        "field_updates": [{"name": name, "options_to_add": list(labels)} for name, labels in plan.field_updates.items()],
        "operations": operations,
    }
    if applied is not None:
        output["applied"] = applied
    return output


def _load_json_file(path: Path, label: str) -> Any:
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise SyncError(f"cannot read {label} file {path}: {exc}") from exc


def run(args: argparse.Namespace) -> dict[str, Any]:
    mapping = load_mapping(Path(args.mapping_file))
    owners = load_owners(Path(args.owners_file), mapping)
    project_ref = parse_project_url(args.project_url) if args.project_url else None
    if args.project_file:
        if args.apply:
            raise SyncError("--apply cannot be used with --project-file; provide a live Project instead")
    issues = load_issues_file(Path(args.issues_file), mapping["repo"]) if args.issues_file else fetch_open_issues(mapping["repo"])

    if args.project_file:
        project_raw = _load_json_file(Path(args.project_file), "project")
        snapshot = normalize_project_snapshot(project_raw, args.project_url)
    else:
        if project_ref is None:
            raise SyncError("--project-url is required for a live Project")
        snapshot = fetch_project(project_ref)

    plan = build_plan(mapping, issues, snapshot, owners=owners)
    applied = apply_plan(plan) if args.apply else None
    return plan_to_json(plan, "apply" if args.apply else "dry-run", applied)


def make_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Plan or apply open-issue routing updates to a GitHub Project")
    parser.add_argument("--project-url", help="GitHub Project URL under /orgs/OWNER/projects/N or /users/OWNER/projects/N")
    parser.add_argument("--mapping-file", default=str(ROOT / "data" / "issue-areas.json"))
    parser.add_argument("--owners-file", default=str(ROOT / "data" / "area-owners.json"))
    parser.add_argument("--issues-file", help="offline open-issues JSON fixture")
    parser.add_argument("--project-file", help="offline Project snapshot JSON fixture (dry-run only)")
    parser.add_argument("--apply", action="store_true", help="apply the validated plan; default is dry-run")
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = make_parser()
    args = parser.parse_args(argv)
    try:
        result = run(args)
    except SyncError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
