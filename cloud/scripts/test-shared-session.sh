#!/usr/bin/env bash
set -euo pipefail

# End-to-end check of shared cloud sessions against a running control plane
# with local auth (npm run cloud:local, or an isolated stack). It plays three
# people through the public API: Alice owns a session and shares it, Bob
# redeems an editor link, and Carol a view-only link. It checks the share
# links, the live viewer list and composer lease on live=1 event streams, the
# hard lock on sending, the handoff to the next person in line, epoch
# fencing, and prompt attribution. Every account and project it makes is
# new, so it never touches existing data.
#
#   AO_CLOUD_URL=http://127.0.0.1:8081 bash cloud/scripts/test-shared-session.sh

api="${AO_CLOUD_URL:-http://127.0.0.1:8081}/api/cloud/v1"
run="$(date +%s)-$RANDOM"
work="$(mktemp -d)"
stream_pids=()
cleanup() {
	for pid in ${stream_pids[@]+"${stream_pids[@]}"}; do kill "$pid" 2>/dev/null || true; done
}
trap cleanup EXIT

pass=0
check() {
	local name="$1"
	shift
	if "$@"; then
		pass=$((pass + 1))
		printf '  ok   %s\n' "$name"
	else
		printf '  FAIL %s\n       checked: %s\n' "$name" "$*" >&2
		exit 1
	fi
}

# call METHOD PATH TOKEN [BODY] -> prints "STATUS BODY". The body is written
# to a file and the status read separately, so nothing in a response can be
# mistaken for the status.
call() {
	local method="$1" path="$2" token="$3" body="${4:-}"
	# Calls run in subshells, so each names its own key and file.
	local id
	id="$(uuidgen)"
	local out="$work/response-$id.json"
	local args=(-sS -o "$out" -w '%{http_code}' -X "$method" -H "Authorization: Bearer $token" -H "Idempotency-Key: $id")
	if [[ -n "$body" ]]; then args+=(-H 'Content-Type: application/json' --data-binary "$body"); fi
	local status
	status="$(curl "${args[@]}" "$api$path")"
	printf '%s %s\n' "$status" "$(jq -c . "$out" 2>/dev/null || tr -d '\n' <"$out")"
}
status_of() { cut -d' ' -f1 <<<"$1"; }
body_of() { cut -d' ' -f2- <<<"$1"; }

register() {
	local name="$1" slug="$2"
	curl -sS -X POST -H 'Content-Type: application/json' \
		-d "{\"email\":\"$slug-$run@share.test\",\"displayName\":\"$name\",\"password\":\"share-test-password\",\"orgSlug\":\"$slug-$run\",\"orgName\":\"$name Org\"}" \
		"$api/auth/local/register"
}

echo "Shared session end-to-end against $api"
alice_auth="$(register Alice alice)"
bob_auth="$(register Bob bob)"
carol_auth="$(register Carol carol)"
alice="$(jq -r .token <<<"$alice_auth")"
bob="$(jq -r .token <<<"$bob_auth")"
carol="$(jq -r .token <<<"$carol_auth")"
org="$(jq -r '.organizations[0].id' <<<"$alice_auth")"
alice_id="$(jq -r .user.id <<<"$alice_auth")"
bob_id="$(jq -r .user.id <<<"$bob_auth")"
check "three people signed up" test -n "$alice" -a -n "$bob" -a -n "$carol" -a "$org" != null

# A session needs its creator's coding-agent credential; local development
# accepts a placeholder without validating it, and nothing runs an agent here.
credential="$(call PUT /me/providers/claude-code "$alice" '{"credentialType":"api_key","secret":"sk-ant-shared-session-test"}')"
check "Alice saved a coding-agent credential ($(status_of "$credential"))" test "$(status_of "$credential")" -lt 300

# Request bodies are built with jq rather than escaped quotes: inside nested
# command substitutions, macOS's bash 3.2 keeps the backslashes.
redeem_body() { jq -nc --arg org "$org" --arg token "$1" '{orgId: $org, token: $token}'; }
message_body() {
	if [[ -n "${2:-}" ]]; then jq -nc --arg text "$1" --argjson epoch "$2" '{text: $text, composerEpoch: $epoch}'
	else jq -nc --arg text "$1" '{text: $text}'; fi
}
redeem() { call POST /share-links/redeem "$1" "$(redeem_body "$2")"; }
send() { call POST "/orgs/$org/sessions/$session/messages" "$1" "$(message_body "$2" "${3:-}")"; }
composer() { call POST "/orgs/$org/sessions/$session/composer/$2" "$1" "${3:-}"; }

project="$(body_of "$(call POST "/orgs/$org/projects/standalone" "$alice" '{"displayName":"Shared session test"}')" | jq -r '.project.id')"
session_body="$(jq -nc --arg project "$project" '{projectId: $project, kind: "worker", harness: "claude-code", displayName: "Pairing", interfaceMode: "chat"}')"
session_response="$(call POST "/orgs/$org/sessions" "$alice" "$session_body")"
session="$(body_of "$session_response" | jq -r '.session.id')"
check "Alice made a session ($(status_of "$session_response"))" test "$session" != null -a -n "$session"

link() {
	call POST "/orgs/$org/projects/$project/shares" "$alice" "$(jq -nc --arg session "$session" --arg role "$1" \
		'{sessionId: $session, role: $role, accessScope: "anyone", interaction: "interact", modeCap: "standard", expiresInHours: 24}')"
}
editor_link="$(link editor)"
viewer_link="$(link viewer)"
editor_token="$(body_of "$editor_link" | jq -r .link.token)"
viewer_token="$(body_of "$viewer_link" | jq -r .link.token)"
check "Alice made an editor link and a viewer link" test "$(status_of "$editor_link")" = 201 -a "$(status_of "$viewer_link")" = 201
guest_link="$(call POST "/orgs/$org/projects/$project/shares" "$bob" "$(jq -nc --arg session "$session" '{sessionId: $session}')")"
check "a guest cannot make links for Alice's project" test "$(status_of "$guest_link")" != 201

redeemed="$(redeem "$bob" "$editor_token")"
check "Bob redeemed the editor link into this session" test "$(body_of "$redeemed" | jq -r .shared.sessionId)" = "$session"
check "Carol redeemed the viewer link" test "$(status_of "$(redeem "$carol" "$viewer_token")")" = 200
own="$(redeem "$alice" "$editor_token")"
check "Alice opening her own link goes to the session" test "$(body_of "$own" | jq -r '"\(.shared.member) \(.shared.sessionId)"')" = "true $session"
check "a made-up token is refused" test "$(status_of "$(redeem "$carol" not-a-token)")" != 200
check "Bob sees the shared session" test "$(body_of "$(call GET "/orgs/$org/shared/projects/$project/sessions" "$bob")" | jq -r '.sessions[0].id')" = "$session"

# Live streams: each one lists its owner as a viewer while it stays open.
open_live() {
	curl -sS -N -H "Authorization: Bearer $1" -H 'Accept: text/event-stream' \
		"$api/orgs/$org/sessions/$session/events?live=1&types=session.live" >"$work/$2.sse" &
	stream_pids+=("$!")
	# Closing a stream on purpose is not worth a "Terminated" line.
	disown "$!"
}
last_live() { grep '^data: ' "$work/$1.sse" | tail -n1 | cut -c7-; }
wait_live() {
	local who="$1" filter="$2"
	for _ in $(seq 1 50); do
		if [[ -s "$work/$who.sse" ]] && last_live "$who" | jq -e "$filter" >/dev/null 2>&1; then return 0; fi
		sleep 0.2
	done
	echo "    last frame for $who: $(last_live "$who")" >&2
	return 1
}
open_live "$alice" alice
open_live "$bob" bob
check "Alice's stream lists Alice and Bob" wait_live alice '([.viewers[].name] | index("Alice") and index("Bob"))'
check "Alice may take over" wait_live alice '.canTakeOver == true'
check "Bob, a guest, may not" wait_live bob '.canTakeOver == false'
check "the composer starts free" wait_live alice '.composer.holder == null'

chat_settings() { call PUT "/orgs/$org/sessions/$session/chat-settings" "$1" "$(jq -nc --arg model "$2" '{model: $model, reasoningEffort: "high"}')"; }
check "Alice, the creator, sets the session's model" test "$(body_of "$(chat_settings "$alice" opus)" | jq -r .chatSettings.model)" = opus
check "Bob, a guest, cannot change it" test "$(status_of "$(chat_settings "$bob" haiku)")" = 403
check "Bob's stream shows Alice's model, not his to change" wait_live bob '.chatSettings.model == "opus" and .chatSettings.reasoningEffort == "high" and .canConfigure == false'
check "Alice's stream lets her change it" wait_live alice '.canConfigure == true'

acquire="$(composer "$bob" acquire)"
bob_epoch="$(body_of "$acquire" | jq -r .composer.epoch)"
check "Bob takes the composer" test "$(body_of "$acquire" | jq -r .granted)" = true
check "Alice's stream shows Bob writing" wait_live alice ".composer.holder.userId == \"$bob_id\""
draft_body="$(jq -nc --argjson epoch "$bob_epoch" '{epoch: $epoch, text: "fix the flaky test", cursor: 7}')"
check "Bob shares his draft as he types" test "$(body_of "$(composer "$bob" draft "$draft_body")" | jq -r .granted)" = true
check "Alice watches Bob's draft and caret live" wait_live alice '.composer.draft.text == "fix the flaky test" and .composer.draft.cursor == 7'
check "Alice cannot write into Bob's draft" test "$(body_of "$(composer "$alice" draft "$draft_body")" | jq -r .granted)" = false
check "Alice cannot take it while Bob writes" test "$(body_of "$(composer "$alice" acquire)" | jq -r .granted)" = false
locked="$(send "$alice" "from alice without the lease")"
check "Alice's send is refused with COMPOSER_LOCKED" test "$(status_of "$locked")" = 409 -a "$(body_of "$locked" | jq -r .code)" = COMPOSER_LOCKED
check "Carol, a viewer, cannot send" test "$(status_of "$(send "$carol" hi)")" = 403
check "Carol, a viewer, cannot take the composer" test "$(status_of "$(composer "$carol" acquire)")" = 403
check "Bob, a guest, cannot take over" test "$(status_of "$(composer "$bob" takeover)")" = 403

queued="$(composer "$alice" queue)"
check "Alice gets in line" test "$(body_of "$queued" | jq -r '.composer.waitlist[0].userId')" = "$alice_id"
check "Bob's stream shows Alice waiting" wait_live bob ".composer.waitlist[0].userId == \"$alice_id\""

sent="$(send "$bob" "from bob" "$bob_epoch")"
check "Bob sends with his grant" test "$(status_of "$sent")" = 202
check "Bob's prompt names him" test "$(body_of "$sent" | jq -r .event.payload.authorName)" = Bob
check "Bob's draft is gone once he sends" wait_live alice '.composer.draft == null'
check "the lease passed to Alice, next in line" wait_live bob ".composer.holder.userId == \"$alice_id\" and .composer.epoch > $bob_epoch"
check "Bob's old grant is fenced off" test "$(body_of "$(send "$bob" stale "$bob_epoch")" | jq -r .code)" = COMPOSER_LOCKED

alice_epoch="$(last_live bob | jq -r .composer.epoch)"
check "Alice sends with the grant handed to her" test "$(status_of "$(send "$alice" "from alice" "$alice_epoch")")" = 202
check "after Alice's send the composer is free" wait_live bob '.composer.holder == null'
free_send="$(call POST "/orgs/$org/sessions/$session/messages" "$bob" "$(jq -nc '{text: "no grant needed", model: "haiku"}')")"
check "with the composer free, a send without a grant works" test "$(status_of "$free_send")" = 202
check "Bob's turn runs with Alice's model, not the one he asked for" test "$(body_of "$free_send" | jq -r .event.payload.model)" = opus

taken="$(composer "$bob" acquire)"
taken_epoch="$(body_of "$taken" | jq -r .composer.epoch)"
check "Alice, the creator, takes over from Bob" test "$(body_of "$(composer "$alice" takeover)" | jq -r .granted)" = true
renew_body="$(jq -nc --argjson epoch "$taken_epoch" '{epoch: $epoch, typing: true}')"
check "Bob's renewal after the take-over is lost" test "$(body_of "$(composer "$bob" renew "$renew_body")" | jq -r .granted)" = false

events="$(body_of "$(call GET "/orgs/$org/sessions/$session/chat-events?types=chat.user_message" "$carol")")"
check "Carol sees who wrote each prompt" test "$(jq -r '[.events[].payload.authorName] | join(",")' <<<"$events")" = "Bob,Alice,Bob"

kill "${stream_pids[1]}" 2>/dev/null || true
check "closing Bob's stream drops him from the viewers" wait_live alice '([.viewers[].name] | index("Bob")) == null'

revoked_link="$(body_of "$editor_link" | jq -r .link.id)"
check "Alice revokes the editor link" test "$(status_of "$(call POST "/orgs/$org/projects/$project/shares/$revoked_link/revoke" "$alice")")" = 204
check "a revoked link cannot be redeemed" test "$(status_of "$(redeem "$carol" "$editor_token")")" != 200

echo "All $pass checks passed."
