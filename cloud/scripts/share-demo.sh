#!/usr/bin/env bash
set -euo pipefail

# End-to-end demo of deep-link session sharing against the local Docker
# control plane (npm run cloud:local). It signs in as BOTH seeded accounts —
# dev@local.test (owner) and viewer@local.test (recipient) — and walks the
# share design: mint a "Can view" link, preview it, redeem it once, prove the
# recipient can read but every write path is refused and that replay / wrong
# secret / wrong account / expiry are all a generic 403; then mint a "Can
# interact" link and prove the recipient can message the agent; then have the
# recipient remove the session from their list and lose access.
#
# Usage:
#   bash cloud/scripts/share-demo.sh               # full scripted demo
#   bash cloud/scripts/share-demo.sh --mint-only   # just print a fresh link for the desktop demo
#
# It never overwrites existing credentials on the dev account: a placeholder
# agent key is only set when the org has none, so a session can be created.

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export AO_CLOUD_URL="http://127.0.0.1:${AO_CLOUD_PORT:-8081}"
export AO_SHARE_DEMO_MODE="${1:-full}"
export AO_SHARE_DEMO_COMPOSE_DIR="$repository_root"

exec python3 - <<'PY'
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

BASE = os.environ["AO_CLOUD_URL"]
MODE = os.environ["AO_SHARE_DEMO_MODE"]
OWNER = ("dev@local.test", "localdevpass123")
VIEWER = ("viewer@local.test", "localviewerpass123")
failures = []


def call(method, path, token=None, body=None, idempotent=False):
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(BASE + path, data=data, method=method)
    request.add_header("Content-Type", "application/json")
    if token:
        request.add_header("Authorization", "Bearer " + token)
    if idempotent:
        request.add_header("Idempotency-Key", str(uuid.uuid4()))
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            raw = response.read()
            return response.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as error:
        raw = error.read()
        try:
            return error.code, json.loads(raw)
        except ValueError:
            return error.code, {"raw": raw.decode(errors="replace")}


def check(label, status, want, body=None):
    ok = status == want
    print(f"  {'PASS' if ok else 'FAIL'}  {label:<58} HTTP {status}")
    if not ok:
        failures.append(label)
        print(f"        expected HTTP {want}; body: {json.dumps(body)[:300]}")


def login(email, password):
    status, body = call("POST", "/api/cloud/v1/auth/local/login", body={"email": email, "password": password})
    if status != 200:
        sys.exit(f"Could not sign in as {email} (HTTP {status}). Run `npm run cloud:local` first.")
    return body["token"], body["organizations"][0]["id"], body["user"]


def psql(org_id, sql):
    # Share and audit tables FORCE row-level security, so scope the session to
    # the owner's org exactly like the control plane does.
    script = f"SELECT set_config('ao.org_id', '{org_id}', false) \\g /dev/null\n{sql};\n"
    return subprocess.run(
        ["docker", "exec", "-i", "-e", "PGPASSWORD=ao_cloud_local_owner", "ao-cloud-local-postgres-1",
         "psql", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-U", "ao_cloud_owner", "-d", "ao_cloud", "-At"],
        input=script, check=True, capture_output=True, text=True,
    ).stdout.strip()


def ensure_session(token, org_id):
    status, body = call("GET", f"/api/cloud/v1/orgs/{org_id}/sessions?limit=100", token)
    for session in body.get("items", []):
        if session.get("displayName") == "Share Demo" and not session.get("isTerminated"):
            return session
    status, connections = call("GET", f"/api/cloud/v1/orgs/{org_id}/provider-connections", token)
    if not connections.get("items") and not connections.get("connections"):
        call("PUT", f"/api/cloud/v1/orgs/{org_id}/provider-connections/agents/claude-code", token,
             {"credentialType": "api_key", "secret": "ao-share-demo-development-only"})
    status, body = call("GET", f"/api/cloud/v1/orgs/{org_id}/projects?limit=100", token)
    project = next((p for p in body.get("items", []) if p["displayName"] == "Share Demo"), None)
    if project is None:
        status, body = call("POST", f"/api/cloud/v1/orgs/{org_id}/projects", token, {
            "displayName": "Share Demo",
            "repositoryUrl": "https://github.com/octocat/Hello-World",
            "defaultBranch": "master",
            "config": {},
        }, idempotent=True)
        if status != 201:
            sys.exit(f"Could not create the demo project (HTTP {status}): {body}")
        project = body["project"]
    status, body = call("POST", f"/api/cloud/v1/orgs/{org_id}/sessions", token, {
        "projectId": project["id"], "kind": "orchestrator", "harness": "claude-code",
        "displayName": "Share Demo", "prompt": "", "mode": "trusted",
    }, idempotent=True)
    if status != 201:
        sys.exit(f"Could not create the demo session (HTTP {status}): {body}")
    return body["session"]


def mint(token, org_id, session_id, recipient, access="view"):
    return call("POST", f"/api/cloud/v1/orgs/{org_id}/sessions/{session_id}/share-deeplinks", token,
                {"recipientEmail": recipient, "access": access})


def parts(deep_link):
    # ao-app://share/<orgId>/<linkId>#<secret>
    rest, secret = deep_link[len("ao-app://share/"):].split("#", 1)
    org_id, link_id = rest.split("/", 1)
    return {"orgId": org_id, "linkId": link_id, "token": secret}


owner_token, owner_org, owner_user = login(*OWNER)
viewer_token, _, viewer_user = login(*VIEWER)
session = ensure_session(owner_token, owner_org)
sid = session["id"]
prefix = f"/api/cloud/v1/orgs/{owner_org}/sessions/{sid}"

if MODE == "--mint-only":
    status, body = mint(owner_token, owner_org, sid, VIEWER[0])
    if status != 201:
        sys.exit(f"Mint failed (HTTP {status}): {body}")
    share = body["share"]
    print(f"Session : {session['displayName']} ({sid})")
    print(f"For     : {share['recipient']} as {share['role']}, expires {share['expiresAt']}")
    print(share["deepLink"])
    sys.exit(0)

print(f"\nOwner  : {owner_user['email']}  (org {owner_org})")
print(f"Viewer : {viewer_user['email']}")
print(f"Session: {session['displayName']} ({sid})\n")

print("1. Owner mints a read-only deep link for the viewer")
status, body = mint(owner_token, owner_org, sid, VIEWER[0])
check("owner creates share deep link", status, 201, body)
share = body["share"]
print(f"        {share['deepLink'][:60]}...  role={share['role']} expires={share['expiresAt']}")
link = parts(share["deepLink"])
status, body = mint(viewer_token, owner_org, sid, OWNER[0])
check("non-member cannot mint a link for someone else's session", status, 403, body)

print("\n2. Recipient previews (consent dialog data) — does not consume")
status, body = call("POST", "/api/cloud/v1/share-deeplinks/preview", viewer_token, link)
check("viewer previews invitation", status, 200, body)
if status == 200:
    invite = body["invite"]
    print(f"        \"{invite['inviterName']} invites you to '{invite['sessionName']}' as {invite['role'].upper()}\"")
status, body = call("POST", "/api/cloud/v1/share-deeplinks/preview", viewer_token, link)
check("preview is repeatable (link still pending)", status, 200, body)

print("\n3. Denials are a generic 403")
status, body = call("POST", "/api/cloud/v1/share-deeplinks/redeem", viewer_token, {**link, "token": link["token"] + "x"})
check("wrong secret", status, 403, body)
status, body = call("POST", "/api/cloud/v1/share-deeplinks/redeem", owner_token, link)
check("owner cannot redeem own link", status, 403, body)
status, other = mint(owner_token, owner_org, sid, "someone-else@local.test")
status, body = call("POST", "/api/cloud/v1/share-deeplinks/redeem", viewer_token, parts(other["share"]["deepLink"]))
check("link addressed to a different email", status, 403, body)
status, expiring = mint(owner_token, owner_org, sid, VIEWER[0])
expiring_link = parts(expiring["share"]["deepLink"])
psql(owner_org, "UPDATE ao_project_share_links SET created_at = now() - interval '20 minutes', "
     f"expires_at = now() - interval '10 minutes' WHERE id = '{expiring_link['linkId']}'")
status, body = call("POST", "/api/cloud/v1/share-deeplinks/redeem", viewer_token, expiring_link)
check("expired link", status, 403, body)

print("\n4. Recipient accepts — atomic pending -> redeemed")
status, body = call("POST", "/api/cloud/v1/share-deeplinks/redeem", viewer_token, link)
check("viewer redeems", status, 200, body)
if status == 200:
    grant = body["shared"]["grant"]
    print(f"        grant role={grant['role']} modeCap={grant.get('modeCap')}")
status, body = call("POST", "/api/cloud/v1/share-deeplinks/redeem", viewer_token, link)
check("replaying the same link", status, 403, body)

print("\n5. Recipient can READ the session")
status, body = call("GET", "/api/cloud/v1/shared/projects", viewer_token)
check("session listed under shared-with-me", status, 200, body)
if not any(item.get("sessionId") == sid for item in body.get("shared", [])):
    failures.append("shared list contains the session")
    print("  FAIL  shared list does not contain the session")
status, body = call("GET", prefix, viewer_token)
check("read session", status, 200, body)
status, body = call("GET", prefix + "/chat-events?after=0&limit=10", viewer_token)
check("read chat history", status, 200, body)

print("\n6. Recipient CANNOT write")
status, body = call("POST", prefix + "/messages", viewer_token, {"text": "rm -rf /"}, idempotent=True)
check("send a message to the agent", status, 403, body)
status, body = call("PUT", prefix + "/workspace/file", viewer_token, {"path": "README", "content": "x"})
check("write a workspace file", status, 403, body)
status, body = call("PUT", prefix + "/workspace/review/file", viewer_token,
                    {"path": "README", "content": "x", "expectedFileFingerprint": "f"})
check("write through the review editor", status, 403, body)
status, body = call("POST", prefix + "/browser/aHR0cDovL2xvY2FsaG9zdDozMDAw/api", viewer_token, {})
check("POST through the session browser proxy", status, 403, body)
status, body = call("DELETE", prefix, viewer_token)
check("delete the session", status, 403, body)
status, body = call("POST", prefix + "/restore", viewer_token)
check("restore the session", status, 403, body)
status, body = call("POST", f"/api/cloud/v1/orgs/{owner_org}/projects/{session['projectId']}/shares", viewer_token,
                    {"sessionId": sid, "role": "editor"})
check("re-share / escalate to editor", status, 403, body)

print("\n7. Owner shares again with \"Can interact\" — recipient can work in the session")
status, body = mint(owner_token, owner_org, sid, VIEWER[0], access="interact")
check("owner creates an interact link", status, 201, body)
interact_link = parts(body["share"]["deepLink"])
status, body = call("POST", "/api/cloud/v1/share-deeplinks/preview", viewer_token, interact_link)
check("preview shows editor role", 200 if body.get("invite", {}).get("role") == "editor" else status, 200, body)
status, body = call("POST", "/api/cloud/v1/share-deeplinks/redeem", viewer_token, interact_link)
check("viewer redeems the interact link", status, 200, body)
grant_id = body.get("shared", {}).get("grant", {}).get("id", "")
status, body = call("POST", prefix + "/messages", viewer_token, {"text": "echo hello from the shared user"}, idempotent=True)
check("recipient CAN message the agent", status, 202, body)
status, body = call("DELETE", prefix, viewer_token)
check("recipient still cannot delete the owner's session", status, 403, body)
status, body = mint(owner_token, owner_org, sid, VIEWER[0], access="admin")
check("unknown access level is rejected", status, 422, body)

print("\n8. Recipient removes the session from their list")
status, body = call("DELETE", f"/api/cloud/v1/shared/grants/{grant_id}", owner_token)
check("someone else cannot remove the recipient's copy", status, 404, body)
status, body = call("DELETE", f"/api/cloud/v1/shared/grants/{grant_id}", viewer_token)
check("recipient removes their copy", status, 204, body)
status, body = call("GET", "/api/cloud/v1/shared/projects", viewer_token)
if any(item.get("sessionId") == sid for item in body.get("shared", [])):
    failures.append("session gone from shared list")
    print("  FAIL  session still in the recipient's shared list")
else:
    print("  PASS  %-58s HTTP %s" % ("session gone from the recipient's shared list", status))
status, body = call("GET", prefix, viewer_token)
check("recipient lost access", status, 403, body)
status, body = call("GET", prefix, owner_token)
check("owner's session is untouched", status, 200, body)
status, body = call("DELETE", f"/api/cloud/v1/shared/grants/{grant_id}", viewer_token)
check("removing twice", status, 404, body)

print("\n9. Audit log (ao_audit_events)")
for row in psql(owner_org, "SELECT to_char(created_at, 'HH24:MI:SS'), action, metadata::text FROM ao_audit_events "
                "WHERE action LIKE 'share.%' ORDER BY id DESC LIMIT 14").splitlines()[::-1]:
    print("        " + row.replace("|", "  "))

print()
if failures:
    sys.exit(f"{len(failures)} check(s) FAILED: {failures}")
print("All checks passed: view links are read-only, interact links can work in the session, "
      "each works once for the named recipient, and the recipient can remove their copy.")
PY
