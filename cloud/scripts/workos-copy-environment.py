#!/usr/bin/env python3
"""Copy WorkOS users, organizations, and memberships into another environment.

AO keys accounts by the WorkOS ID a user had when AO first saw them. This copies
every user and organization from a source WorkOS environment into a target one,
setting each copy's external_id to its source ID. The control plane reads that
external_id at sign-in, so a copied user keeps their AO account, organizations,
and repository grants. Passwords are not copied; WorkOS cannot export them.

The copy is idempotent: records already linked by external_id are left alone,
so run it again after cutover to pick up anyone who signed up in between. It
prints counts and record IDs only, never emails or names.

Usage:
    WORKOS_SOURCE_API_KEY=... WORKOS_TARGET_API_KEY=... \\
        python3 cloud/scripts/workos-copy-environment.py            # dry run
    ... python3 cloud/scripts/workos-copy-environment.py --apply    # write

A target user that already exists with the same email but no external_id is
reported and skipped. Pass --link-existing to give it the source ID when both
users have verified that email; that user then signs in to their original AO
account instead of the one they created in the target environment.
"""

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

API = "https://api.workos.com"


class WorkOS:
    def __init__(self, api_key: str):
        self.api_key = api_key

    def request(self, method: str, path: str, body=None):
        data = json.dumps(body).encode() if body is not None else None
        for attempt in range(6):
            request = urllib.request.Request(
                API + path,
                data=data,
                method=method,
                headers={
                    "Authorization": f"Bearer {self.api_key}",
                    "Content-Type": "application/json",
                },
            )
            try:
                with urllib.request.urlopen(request, timeout=30) as response:
                    payload = response.read()
                    return json.loads(payload) if payload else None
            except urllib.error.HTTPError as error:
                if error.code == 429 or error.code >= 500:
                    time.sleep(min(2**attempt, 30))
                    continue
                detail = error.read().decode(errors="replace")[:300]
                raise RuntimeError(f"{method} {path.split('?')[0]}: {error.code} {detail}") from None
        raise RuntimeError(f"{method} {path.split('?')[0]}: retries exhausted")

    def list(self, path: str, **params):
        items, after = [], None
        while True:
            query = dict(params, limit=100)
            if after:
                query["after"] = after
            page = self.request("GET", f"{path}?{urllib.parse.urlencode(query, doseq=True)}")
            items.extend(page["data"])
            after = (page.get("list_metadata") or {}).get("after")
            if not after:
                return items


def memberships(client: WorkOS, organizations):
    result = []
    for organization in organizations:
        result.extend(
            client.list(
                "/user_management/organization_memberships",
                organization_id=organization["id"],
                statuses="active",
            )
        )
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--apply", action="store_true", help="write to the target environment")
    parser.add_argument("--link-existing", action="store_true", help="set external_id on matching target users")
    args = parser.parse_args()

    source_key = os.environ.get("WORKOS_SOURCE_API_KEY", "").strip()
    target_key = os.environ.get("WORKOS_TARGET_API_KEY", "").strip()
    if not source_key or not target_key:
        print("WORKOS_SOURCE_API_KEY and WORKOS_TARGET_API_KEY are required", file=sys.stderr)
        return 2
    if source_key == target_key:
        print("source and target API keys must differ", file=sys.stderr)
        return 2
    source, target = WorkOS(source_key), WorkOS(target_key)
    mode = "apply" if args.apply else "dry run"
    counts = {}

    def count(name):
        counts[name] = counts.get(name, 0) + 1

    def write(record_id, method, path, body):
        # One rejected record must not stop the rest of the copy; report it
        # and carry on, and the next run retries it.
        try:
            return target.request(method, path, body)
        except RuntimeError as error:
            print(f"failed {record_id}: {error}")
            count("failed")
            return None

    source_orgs = source.list("/organizations")
    target_orgs = target.list("/organizations")
    org_map = {org["external_id"]: org["id"] for org in target_orgs if org.get("external_id")}
    for org in source_orgs:
        if org["id"] in org_map:
            count("organizations already linked")
            continue
        body = {
            "name": org["name"],
            "external_id": org["id"],
            "metadata": org.get("metadata") or {},
        }
        domains = [
            {"domain": domain["domain"], "state": domain.get("state") or "pending"}
            for domain in org.get("domains") or []
        ]
        if domains:
            body["domain_data"] = domains
        if args.apply:
            created = write(org["id"], "POST", "/organizations", body)
            if created is None:
                continue
            org_map[org["id"]] = created["id"]
        count("organizations created")

    source_users = source.list("/user_management/users")
    target_users = target.list("/user_management/users")
    user_map = {user["external_id"]: user["id"] for user in target_users if user.get("external_id")}
    by_email = {user["email"].lower(): user for user in target_users}
    for user in source_users:
        if user["id"] in user_map:
            count("users already linked")
            continue
        existing = by_email.get(user["email"].lower())
        if existing:
            if existing.get("external_id"):
                print(f"skip {user['id']}: target {existing['id']} is linked to another user")
                count("users skipped (conflict)")
                continue
            if not args.link_existing:
                print(f"skip {user['id']}: target {existing['id']} has the same email (use --link-existing)")
                count("users skipped (exists)")
                continue
            # A shared email only proves identity once both users verified it.
            # Otherwise anyone who registered the email in the target first
            # would take over the source user's AO account.
            if not existing.get("email_verified") or not user.get("email_verified"):
                print(f"skip {user['id']}: target {existing['id']} shares an email that is not verified on both sides")
                count("users skipped (unverified)")
                continue
            if args.apply and write(
                user["id"], "PUT", f"/user_management/users/{existing['id']}", {"external_id": user["id"]}
            ) is None:
                continue
            user_map[user["id"]] = existing["id"]
            count("users linked")
            continue
        body = {
            "email": user["email"],
            "email_verified": bool(user.get("email_verified")),
            "external_id": user["id"],
        }
        for field in ("first_name", "last_name"):
            if user.get(field):
                body[field] = user[field]
        if args.apply:
            created = write(user["id"], "POST", "/user_management/users", body)
            if created is None:
                continue
            user_map[user["id"]] = created["id"]
        count("users created")

    target_memberships = {
        (membership["user_id"], membership["organization_id"])
        for membership in memberships(target, [{"id": org_id} for org_id in org_map.values()])
    }
    for membership in memberships(source, source_orgs):
        user_id = user_map.get(membership["user_id"])
        org_id = org_map.get(membership["organization_id"])
        if not args.apply and (user_id is None or org_id is None):
            # A dry run has not created the user or organization yet.
            count("memberships created")
            continue
        if user_id is None or org_id is None:
            print(f"skip membership {membership['id']}: user or organization was not copied")
            count("memberships skipped")
            continue
        if (user_id, org_id) in target_memberships:
            count("memberships already present")
            continue
        if args.apply and write(
            membership["id"],
            "POST",
            "/user_management/organization_memberships",
            {
                "user_id": user_id,
                "organization_id": org_id,
                "role_slug": (membership.get("role") or {}).get("slug") or "member",
            },
        ) is None:
            continue
        count("memberships created")

    print(f"{mode}: source has {len(source_users)} users, {len(source_orgs)} organizations")
    for name, value in sorted(counts.items()):
        print(f"  {name}: {value}")
    return 1 if counts.get("failed") else 0


if __name__ == "__main__":
    sys.exit(main())
