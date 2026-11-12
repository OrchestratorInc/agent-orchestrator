#!/usr/bin/env bash
# Moves one session local -> Cloud -> local -> Cloud -> local, against a real
# AO daemon and a real AO Cloud control plane with Docker sandboxes. At every
# hop it checks the destination's branch, its HEAD commit and every file that
# is not ignored, and that ignored files stayed behind.
#
# GitHub is replaced by a git server container on the compose network
# (AO_CLOUD_DEVELOPMENT_GITHUB_URL); the local repository reaches the same
# server through url.insteadOf. The local agent is HARNESS (default
# claude-code, signed in on this machine); the Cloud agent is claude-code
# with a placeholder key, so it never runs a turn.
#
# Usage: AO_CONTROL_PLANE=<ao-control-plane checkout> test/session-move/cloud-round-trip.sh
# Needs go, git, python3, curl, Docker with Compose and buildx, the agent CLI.
# Builds ao-move-control-plane:local and ao-move-worker:local when missing.
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/../.." && pwd)
cp_root=$(cd "${AO_CONTROL_PLANE:?set AO_CONTROL_PLANE to an ao-control-plane checkout}" && pwd)
source "$cp_root/scripts/lib/docker-local.sh"
CP_IMAGE=ao-move-control-plane:local
WORKER_IMAGE=ao-move-worker:local
HARNESS=${HARNESS:-claude-code}
SESSION_LABEL=${AO_SESSION_ID:-cloud-round-trip}

# Compose binds data directories from here, so it must be inside $HOME, which
# Docker Desktop and colima share with their VM.
state_root="${AO_DATA_DIR:-$HOME/.ao}"
mkdir -p "$state_root"
W=$(mktemp -d "$state_root/cloud-move.XXXXXX")
project_name="ao-move-$$"
pids=()

fail() { echo "FAIL: $*" >&2; exit 1; }
step() { echo "== $*"; }
free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {'d': d}))" "$1"; }
compose() { docker compose --project-directory "$cp_root" -f "$cp_root/compose.yaml" -f "$W/compose.move.yaml" "$@"; }

cleanup() {
	local status=$?
	[[ -n "${passed:-}" ]] || status=1
	if ((status != 0)); then
		compose logs migrate control-plane 2>&1 | tail -80 >&2 || true
		for c in $(docker ps -aq --filter "label=ao.docker.namespace=$project_name"); do docker logs "$c" 2>&1 | tail -40 >&2 || true; done
	fi
	for pid in "${pids[@]:-}"; do [[ -n "$pid" ]] && kill "$pid" 2>/dev/null || true; done
	wait 2>/dev/null || true
	pkill -f "$W/" 2>/dev/null || true
	ao_docker_remove_workers "$project_name" >/dev/null 2>&1 || true
	compose down --volumes --remove-orphans >/dev/null 2>&1 || true
	ao_docker_remove_workspaces "$project_name" >/dev/null 2>&1 || true
	if [[ "${KEEP:-}" == "1" ]]; then echo "kept $W"; else rm -rf "$W"; fi
	exit "$status"
}
trap cleanup EXIT

export GIT_CONFIG_GLOBAL="$W/gitconfig" GIT_CONFIG_NOSYSTEM=1
printf '[user]\n\tname = Move Test\n\temail = move@example.com\n[init]\n\tdefaultBranch = main\n' > "$GIT_CONFIG_GLOBAL"

step "images"
if ! docker image inspect "$CP_IMAGE" >/dev/null 2>&1; then
	docker buildx build --load --target control-plane -t "$CP_IMAGE" "$cp_root"
fi
if ! docker image inspect "$WORKER_IMAGE" >/dev/null 2>&1; then
	docker buildx build --load --target worker --build-arg AGENT_BROWSER=0 -t "$WORKER_IMAGE" "$cp_root"
fi

step "build ao and the git server"
(cd "$repo_root/backend" && go build -o "$W/bin/ao" ./cmd/ao)
mkdir -p "$W/gitserver/src" "$W/gitserver/bin"
cat > "$W/gitserver/src/main.go" <<'GO'
// Serves bare repositories under os.Args[1] over smart HTTP, plus the one
// GitHub API call the control plane makes when a project is created.
package main

import (
	"io"
	"log"
	"net/http"
	"net/http/cgi"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := os.Args[1]
	http.HandleFunc("/api/repos/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/repos/")
		if _, err := os.Stat(filepath.Join(root, name+".git")); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"permissions":{"push":true}}`)
	})
	http.Handle("/", &cgi.Handler{Path: "/usr/bin/git", Args: []string{"http-backend"},
		Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}})
	log.Fatal(http.ListenAndServe(":8080", nil))
}
GO
arch=$(docker info --format '{{.Architecture}}'); [[ "$arch" == "aarch64" ]] && arch=arm64; [[ "$arch" == "x86_64" ]] && arch=amd64
GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -o "$W/gitserver/bin/gitserver" "$W/gitserver/src/main.go"

step "start AO Cloud ($project_name) with a git server in place of GitHub"
export COMPOSE_PROJECT_NAME="$project_name"
AO_CLOUD_PORT=$(free_port) AO_CLOUD_POSTGRES_PORT=$(free_port) AO_CLOUD_LOCAL_MINIO_PORT=$(free_port) GIT_PORT=$(free_port)
export AO_CLOUD_PORT AO_CLOUD_POSTGRES_PORT AO_CLOUD_LOCAL_MINIO_PORT GIT_PORT
export AO_CLOUD_LOCAL_POSTGRES_DATA_DIR="$W/postgres" AO_CLOUD_LOCAL_MINIO_DATA_DIR="$W/minio"
AO_CLOUD_LOCAL_UID=$(id -u) AO_CLOUD_LOCAL_GID=$(id -g)
export AO_CLOUD_LOCAL_UID AO_CLOUD_LOCAL_GID
AO_CLOUD_PROVIDER_SECRET_KEY=$(openssl rand -base64 32) AO_CLOUD_WORKER_SIGNING_KEY=$(openssl rand -hex 32)
export AO_CLOUD_PROVIDER_SECRET_KEY AO_CLOUD_WORKER_SIGNING_KEY
# The engine's own socket group: Docker Desktop's is 0, colima's is not.
AO_CLOUD_DOCKER_GID=$(docker run --rm --label ao.session="$SESSION_LABEL" -v /var/run/docker.sock:/s alpine:latest stat -c %g /s)
export AO_CLOUD_DOCKER_GID AO_CLOUD_DOCKER_WORKER_IMAGE="$WORKER_IMAGE"
export AO_CLOUD_DEVELOPMENT_SKIP_CREDENTIAL_VALIDATION=true
mkdir -p "$AO_CLOUD_LOCAL_MINIO_DATA_DIR"
cat > "$W/compose.move.yaml" <<YAML
services:
  postgres: { labels: { ao.session: "$SESSION_LABEL" } }
  minio: { labels: { ao.session: "$SESSION_LABEL" } }
  minio-init: { labels: { ao.session: "$SESSION_LABEL" } }
  migrate: { image: "$CP_IMAGE", labels: { ao.session: "$SESSION_LABEL" } }
  control-plane:
    image: "$CP_IMAGE"
    labels: { ao.session: "$SESSION_LABEL" }
    environment:
      AO_CLOUD_DEVELOPMENT_GITHUB_URL: http://gitserver:8080
  gitserver:
    image: node:22.23.2-bookworm
    labels: { ao.session: "$SESSION_LABEL" }
    volumes: ["$W/gitserver/bin:/srv/bin:ro"]
    entrypoint: ["/bin/sh", "-c"]
    command: ["git init -q --bare -b main /git/acme/app.git && git -C /git/acme/app.git config http.receivepack true && exec /srv/bin/gitserver /git"]
    ports: ["127.0.0.1:$GIT_PORT:8080"]
YAML
# On first boot Postgres runs its init on a unix socket and passes its health
# check before it accepts TCP, so migrate waits for TCP.
compose up -d --no-build --quiet-pull postgres minio minio-init gitserver
for _ in $(seq 1 120); do compose exec -T postgres pg_isready -h 127.0.0.1 -U ao_cloud_owner -d ao_cloud >/dev/null 2>&1 && break; sleep 1; done
compose up -d --no-build --quiet-pull migrate control-plane
for _ in $(seq 1 90); do curl -fsS "http://127.0.0.1:$AO_CLOUD_PORT/readyz" >/dev/null 2>&1 && break; sleep 1; done
curl -fsS "http://127.0.0.1:$AO_CLOUD_PORT/readyz" >/dev/null || fail "AO Cloud did not become ready"
for _ in $(seq 1 30); do curl -fsS "http://127.0.0.1:$GIT_PORT/acme/app.git/info/refs?service=git-upload-pack" >/dev/null 2>&1 && break; sleep 1; done

GIT_URL="http://127.0.0.1:$GIT_PORT/acme/app.git"
ORIGIN_URL=https://github.com/acme/app
step "seed $ORIGIN_URL and clone it for the local daemon"
git init -q "$W/seed"
printf 'hello\n' > "$W/seed/README.md"
printf 'package app\n' > "$W/seed/app.go"
printf '.env\n' > "$W/seed/.gitignore"
git -C "$W/seed" add -A && git -C "$W/seed" commit -qm seed && git -C "$W/seed" push -q "$GIT_URL" main
git clone -q "$GIT_URL" "$W/local/app"
git -C "$W/local/app" remote set-url origin "$ORIGIN_URL"

cloud() { # METHOD PATH [JSON] [IDEMPOTENCY-KEY]
	local args=(-sS -X "$1" "http://127.0.0.1:$AO_CLOUD_PORT/api/cloud/v1$2" -H "Authorization: Bearer ${TOKEN:-}")
	[[ -n "${3:-}" ]] && args+=(-H 'Content-Type: application/json' --data-binary "$3")
	[[ -n "${4:-}" ]] && args+=(-H "Idempotency-Key: $4")
	curl "${args[@]}"
}
step "AO Cloud user, org, agent key, GitHub token, project"
auth=$(cloud POST /auth/local/register '{"email":"move@example.com","displayName":"Move Test","password":"local-move-password","orgSlug":"move-test","orgName":"Move Test"}')
TOKEN=$(echo "$auth" | json "d['token']") || { echo "$auth"; fail "register"; }
ORG=$(echo "$auth" | json "d['organizations'][0]['id']")
cloud PUT /me/providers/claude-code '{"credentialType":"api_key","secret":"ao-move-development-only"}' >/dev/null
cloud PUT /me/github-pat '{"secret":"ao-move-development-only"}' >/dev/null
features=$(cloud GET /me | json "d['offering']['features']")
[[ "$features" == *session.branch* ]] || fail "offering.features lacks session.branch: $features"
out=$(cloud POST "/orgs/$ORG/projects" "{\"displayName\":\"app\",\"repositoryUrl\":\"$ORIGIN_URL\",\"defaultBranch\":\"main\",\"config\":{}}" project-1)
CLOUD_PROJECT=$(echo "$out" | json "d['project']['id']") || { echo "$out"; fail "cloud project"; }

step "start the local daemon"
PORT=$(free_port)
mkdir -p "$W/daemon"
AO_DATA_DIR="$W/daemon" AO_RUN_FILE="$W/daemon/running.json" AO_PORT=$PORT AO_CLOUD_OFFERING=off \
	"$W/bin/ao" daemon > "$W/daemon.log" 2>&1 &
pids+=($!)
for _ in $(seq 1 100); do curl -fsS "http://127.0.0.1:$PORT/readyz" >/dev/null 2>&1 && break; sleep 0.3; done
api() { # METHOD PATH [JSON]
	if [[ -n "${3:-}" ]]; then
		curl -sS -X "$1" "http://127.0.0.1:$PORT/api/v1$2" -H 'Content-Type: application/json' --data-binary "$3"
	else
		curl -sS -X "$1" "http://127.0.0.1:$PORT/api/v1$2"
	fi
}
LOCAL_PROJECT=$(api POST /projects "{\"path\":\"$W/local/app\"}" | json "d['project']['id']")
# AO keys the project by its origin URL; from here git sends that URL to the git server.
printf '[url "%s"]\n\tinsteadOf = %s\n' "$GIT_URL" "$ORIGIN_URL" >> "$GIT_CONFIG_GLOBAL"

BRANCH=feat/move
# SNAPSHOT prints "<blob id> <path>" for every file on disk that git does not ignore.
SNAPSHOT='cd "$1" && git ls-files -co --exclude-standard --deduplicate | sort | while IFS= read -r f; do [ -e "$f" ] || continue; printf "%s %s\n" "$(git hash-object -- "$f")" "$f"; done'
worktree_for() { git -C "$W/local/app" worktree list --porcelain | awk -v want="branch refs/heads/$BRANCH" '/^worktree /{p=substr($0,10)} $0==want{print p}'; }
remote_tip() { git ls-remote "$GIT_URL" "refs/heads/$BRANCH" | cut -f1; }
session_state() { api GET "/sessions/$1" | json "d['session']['activity']['state']"; }
stop_agent() {
	local state=""
	for _ in $(seq 1 120); do state=$(session_state "$1"); [[ "$state" != "spawning" && -n "$state" ]] && break; sleep 0.5; done
	api POST "/sessions/$1/exit-agent" >/dev/null
	for _ in $(seq 1 120); do [[ "$(session_state "$1")" == "exited" ]] && return 0; sleep 0.5; done
	fail "local session $1 did not stop"
}
cloud_container() { docker ps -q --filter "label=ao.docker.namespace=$project_name" --filter "label=ao.session_id=$1" | head -1; }
cloud_exec() { docker exec -i "$CONTAINER" sh -c "$1" _ "${@:2}"; }
# wait_for_cloud waits until session $1's checkout is on $BRANCH at $2, then sets CONTAINER and CLOUD_REPO.
wait_for_cloud() {
	local id=$1 want=$2 head=""
	CONTAINER=""
	for _ in $(seq 1 240); do
		[[ -z "$CONTAINER" ]] && CONTAINER=$(cloud_container "$id")
		if [[ -n "$CONTAINER" ]]; then
			CLOUD_REPO=$(cloud_exec 'for d in /workspace/repository /workspace/*/ /workspace; do git -C "$d" remote get-url origin >/dev/null 2>&1 && { git -C "$d" rev-parse --show-toplevel; exit 0; }; done' 2>/dev/null || true)
			if [[ -n "$CLOUD_REPO" ]]; then
				head=$(cloud_exec 'git -C "$1" rev-parse HEAD' "$CLOUD_REPO" 2>/dev/null || true)
				[[ "$head" == "$want" ]] && return 0
			fi
		fi
		sleep 1
	done
	cloud GET "/orgs/$ORG/sessions/$id" >&2 || true
	fail "Cloud session $id never checked out $BRANCH at $want (container '${CONTAINER}', repo '${CLOUD_REPO:-}', HEAD '$head')"
}
check_cloud() { # session want-tip want-snapshot-file
	wait_for_cloud "$1" "$2"
	[[ "$(cloud_exec 'git -C "$1" symbolic-ref --short HEAD' "$CLOUD_REPO")" == "$BRANCH" ]] || fail "Cloud checkout is not on $BRANCH"
	cloud_exec "$SNAPSHOT" "$CLOUD_REPO" > "$W/got.snap"
	diff -u "$3" "$W/got.snap" || fail "Cloud files differ from the source"
	cloud_exec 'test ! -e "$1/.env"' "$CLOUD_REPO" || fail "the ignored .env reached Cloud"
	echo "  Cloud $CONTAINER: $BRANCH at ${2:0:12}, $(wc -l < "$3" | tr -d ' ') files identical, .env left behind"
}
check_local() { # want-tip want-snapshot-file
	local wt; wt=$(worktree_for)
	[[ -d "$wt" ]] || fail "no local worktree has $BRANCH checked out"
	[[ "$(git -C "$wt" rev-parse HEAD)" == "$1" ]] || fail "local $BRANCH is $(git -C "$wt" rev-parse HEAD), want $1"
	sh -c "$SNAPSHOT" _ "$wt" > "$W/got.snap"
	diff -u "$2" "$W/got.snap" || fail "local files differ from Cloud"
	[[ "$(cat "$wt/.env" 2>/dev/null)" != "CLOUD_SECRET=1" ]] || fail "Cloud's ignored .env reached the local worktree"
	echo "  local $wt: $BRANCH at ${1:0:12}, $(wc -l < "$2" | tr -d ' ') files identical"
}

# local_to_cloud moves local session $1 to a new Cloud session and sets NEXT to its id.
local_to_cloud() {
	local sid=$1 move="m-$2" wt out tip cid body
	wt=$(worktree_for)
	sh -c "$SNAPSHOT" _ "$wt" > "$W/want.snap"
	out=$(api POST "/sessions/$sid/move/export" "{\"moveId\":\"$move\",\"sourceLabel\":\"MacBook\"}")
	tip=$(echo "$out" | json "d['manifest']['tip']") || { echo "$out"; fail "local export"; }
	[[ "$(remote_tip)" == "$tip" ]] || fail "origin $BRANCH is $(remote_tip), want the exported $tip"
	echo "$out" | python3 -c "import json,sys; json.dump(json.load(sys.stdin), open('$W/export.json','w'))"
	body=$(python3 - "$W/export.json" "$CLOUD_PROJECT" <<'PY'
import json, sys
d = json.load(open(sys.argv[1])); m = d["manifest"]
print(json.dumps({"projectId": sys.argv[2], "kind": "worker", "harness": "claude-code",
                  "displayName": m.get("displayName") or "Move test", "prompt": d["handoffNote"], "branch": m["branch"]}))
PY
)
	out=$(cloud POST "/orgs/$ORG/sessions" "$body" "$move")
	cid=$(echo "$out" | json "d['session']['id']") || { echo "$out"; fail "Cloud create"; }
	out=$(api POST "/sessions/$sid/move/complete" "{\"moveId\":\"$move\",\"orgId\":\"$ORG\",\"sessionId\":\"$cid\"}")
	[[ "$(echo "$out" | json "d['session']['movedTo']['sessionId']")" == "$cid" ]] || { echo "$out"; fail "local complete"; }
	check_cloud "$cid" "$tip" "$W/want.snap"
	NEXT=$cid
}

# cloud_to_local moves Cloud session $1 to a new local session and sets NEXT to its id.
cloud_to_local() {
	local cid=$1 move="m-$2" out tip sid body
	cloud_exec "$SNAPSHOT" "$CLOUD_REPO" > "$W/want.snap"
	out=$(cloud POST "/orgs/$ORG/sessions/$cid/move/export")
	tip=$(echo "$out" | json "d['manifest']['tip']") || { echo "$out"; fail "Cloud export"; }
	[[ "$(echo "$out" | json "d['manifest']['wip']")" == "True" ]] || fail "Cloud export made no WIP commit"
	[[ "$(remote_tip)" == "$tip" ]] || fail "origin $BRANCH is $(remote_tip), want the exported $tip"
	echo "$out" > "$W/export.json"
	body=$(python3 - "$W/export.json" "$LOCAL_PROJECT" "$move" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print(json.dumps({"projectId": sys.argv[2], "moveId": sys.argv[3], "manifest": d["manifest"], "handoffNote": d["handoffNote"]}))
PY
)
	out=$(api POST /sessions/move/import "$body")
	sid=$(echo "$out" | json "d['session']['id']") || { echo "$out"; fail "local import"; }
	check_local "$tip" "$W/want.snap"
	cloud DELETE "/orgs/$ORG/sessions/$cid" >/dev/null
	NEXT=$sid
}

work_local() { # hop
	local wt; wt=$(worktree_for)
	printf 'package app // committed locally, hop %s\n' "$1" > "$wt/app.go"
	git -C "$wt" commit -qam "local work, hop $1"
	printf 'local edit, hop %s\n' "$1" >> "$wt/README.md"
	mkdir -p "$wt/notes" && printf 'new locally, hop %s\n' "$1" > "$wt/notes/hop-$1.md"
	printf 'LOCAL_SECRET=1\n' > "$wt/.env"
}
work_cloud() { # hop
	cloud_exec 'cd "$1" && printf "package app // committed in Cloud, hop %s\n" "$2" > app.go &&
		git -c user.name=Cloud -c user.email=cloud@example.com commit -qam "Cloud work, hop $2" &&
		printf "Cloud edit, hop %s\n" "$2" >> README.md && printf "new in Cloud, hop %s\n" "$2" > "cloud-$2.txt" &&
		rm -f "notes/hop-$(($2 - 1)).md" && printf "CLOUD_SECRET=1\n" > .env' "$CLOUD_REPO" "$1"
}

step "hop 0: a $HARNESS session on this machine, on $BRANCH"
out=$(api POST /sessions "{\"projectId\":\"$LOCAL_PROJECT\",\"kind\":\"worker\",\"harness\":\"$HARNESS\",\"prompt\":\"Reply with the single word OK. Do not run tools or edit files.\",\"displayName\":\"Move test\",\"branch\":\"$BRANCH\"}")
sid=$(echo "$out" | json "d['session']['id']") || { echo "$out"; fail "local spawn"; }
stop_agent "$sid"
work_local 1

step "hop 1: local -> Cloud"
local_to_cloud "$sid" 1; cid=$NEXT
work_cloud 2

step "hop 2: Cloud -> local"
cloud_to_local "$cid" 2; sid=$NEXT
stop_agent "$sid"
work_local 3

step "hop 3: local -> Cloud"
local_to_cloud "$sid" 3; cid=$NEXT
work_cloud 4

step "hop 4: Cloud -> local"
cloud_to_local "$cid" 4; sid=$NEXT

step "the branch carries every hop's work, in order"
wt=$(worktree_for)
git -C "$wt" log --reverse --format=%s main..HEAD > "$W/log"
cat > "$W/want.log" <<'LOG'
local work, hop 1
WIP: uncommitted changes from MacBook
Cloud work, hop 2
WIP: uncommitted changes from AO Cloud
local work, hop 3
WIP: uncommitted changes from MacBook
Cloud work, hop 4
WIP: uncommitted changes from AO Cloud
LOG
diff -u "$W/want.log" "$W/log" || fail "branch history"
sed 's/^/  /' "$W/log"

passed=1
echo "PASS: local -> Cloud -> local -> Cloud -> local; $BRANCH ends at $(git -C "$wt" rev-parse --short HEAD) in local session $sid"
