#!/usr/bin/env bash
set -euo pipefail

# Bakes the Freestyle harness snapshots sessions boot from: one per harness,
# each the sandbox image for that harness plus the worker binaries.
#
# Freestyle cannot import a Docker image, so a snapshot is a booted base VM that
# was provisioned and then frozen (memory and disk). This script runs the RUN
# steps of the sandbox image Dockerfiles (sandbox-image/Sandbox.base.Dockerfile
# plus sandbox-image/harness/<harness>.Dockerfile), in order, inside the VM. A
# version bump in those files therefore reaches Freestyle on the next bake.
#
# The worker binaries come from the control-plane image that will serve these
# sandboxes, so they hash-match what it advertises and a new session launches
# the baked worker instead of self-updating first. Rebake on every release that
# changes the worker.
#
# Run from cloud/. Prints {"<harness>": "<snapshot id>", ...} on stdout; with
# AO_CLOUD_FREESTYLE_UPDATE_SECRET=1 it also writes that map (and the
# claude-code snapshot as default_snapshot) into the Freestyle secret.

# Defaults to the ao-cloud profile for manual runs; a deploy passes an empty
# AWS_PROFILE to use its own ambient credentials.
AWS_PROFILE="${AWS_PROFILE-ao-cloud}"
AWS_REGION="${AWS_REGION:-eu-north-1}"
FREESTYLE_SECRET_ID="${AO_CLOUD_FREESTYLE_SECRET_ID:-ao-cloud/staging/freestyle}"
API_URL="${AO_CLOUD_FREESTYLE_BASE_URL:-https://api.freestyle.sh}"
# The base fixes the VM's size: freestyle/ubuntu-sm is 2 vCPU / 4 GiB / 16 GB.
BASE_SNAPSHOT="${AO_CLOUD_FREESTYLE_BASE_SNAPSHOT:-freestyle/ubuntu-sm}"
HARNESSES="${AO_CLOUD_FREESTYLE_HARNESSES:-claude-code,codex,cursor}"
BASE_DOCKERFILE="${AO_CLOUD_FREESTYLE_BASE_DOCKERFILE:-sandbox-image/Sandbox.base.Dockerfile}"
ARTIFACTS_BUCKET="${AO_CLOUD_ARTIFACTS_BUCKET:-ao-cloud-staging-artifacts}"
UPDATE_SECRET="${AO_CLOUD_FREESTYLE_UPDATE_SECRET:-}"
CP_IMAGE="${AO_CLOUD_CP_IMAGE:-}"
# Freestyle caps one exec at five minutes; each Dockerfile RUN step is one exec.
EXEC_TIMEOUT_MS=300000

log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }
die() { log "error: $*"; exit 1; }

[[ -n "$CP_IMAGE" ]] || die "set AO_CLOUD_CP_IMAGE to the control-plane image whose worker binaries should be baked"
[[ -f "$BASE_DOCKERFILE" ]] || die "missing $BASE_DOCKERFILE (run from cloud/)"
for tool in aws curl docker jq python3; do
	command -v "$tool" >/dev/null || die "$tool is required"
done

aws_cli() {
	if [[ -n "$AWS_PROFILE" ]]; then
		aws --profile "$AWS_PROFILE" --region "$AWS_REGION" "$@"
	else
		aws --region "$AWS_REGION" "$@"
	fi
}

API_KEY="${AO_CLOUD_FREESTYLE_API_KEY:-}"
if [[ -z "$API_KEY" ]]; then
	API_KEY="$(aws_cli secretsmanager get-secret-value --secret-id "$FREESTYLE_SECRET_ID" \
		--query SecretString --output text | jq -r '.api_key // empty')"
fi
[[ -n "$API_KEY" ]] || die "no Freestyle API key (AO_CLOUD_FREESTYLE_API_KEY or $FREESTYLE_SECRET_ID api_key)"

# api METHOD PATH [JSON] prints the response body and fails on a non-2xx status.
# The key travels only in a header file, never in argv.
auth_header="$(mktemp)"
workdir="$(mktemp -d)"
printf 'Authorization: Bearer %s\n' "$API_KEY" >"$auth_header"
chmod 0600 "$auth_header"
cleanup() { rm -rf "$workdir" "$auth_header"; }
trap cleanup EXIT

# Builder VMs are disposable whatever happens; each bake deletes its own.
delete_vm() {
	[[ -n "${1:-}" ]] || return 0
	curl --silent --show-error -X DELETE -H "@$auth_header" "$API_URL/v5/vms/$1" >/dev/null 2>&1 || true
	log "deleted builder VM $1"
}

api() {
	local method="$1" path="$2" body="${3:-}" response status
	local args=(--silent --show-error -X "$method" -H "@$auth_header" -H 'Accept: application/json'
		--write-out '\n%{http_code}' --max-time 330)
	if [[ -n "$body" ]]; then
		args+=(-H 'Content-Type: application/json' --data-binary "$body")
	fi
	response="$(curl "${args[@]}" "$API_URL$path")"
	status="${response##*$'\n'}"
	response="${response%$'\n'*}"
	if [[ "$status" != 2* ]]; then
		log "freestyle $method $path returned $status: ${response:0:500}"
		return 1
	fi
	printf '%s' "$response"
}

# vm_exec VM COMMAND runs COMMAND as root and fails on a nonzero exit.
vm_exec() {
	local vm="$1" command="$2" result code
	result="$(api POST "/v5/vms/$vm/exec-await" "$(jq -n --arg command "$command" --argjson timeout "$EXEC_TIMEOUT_MS" \
		'{command: $command, linuxUser: "root", timeoutMs: $timeout}')")" || return 1
	code="$(jq -r '.statusCode // 0' <<<"$result")"
	if [[ "$code" != 0 ]]; then
		jq -r '.stdout // ""' <<<"$result" | tail -20 >&2
		jq -r '.stderr // ""' <<<"$result" | tail -40 >&2
		return 1
	fi
}

# vm_step runs a long provisioning step detached inside the VM and polls for
# its exit status. A step that downloads hundreds of MB can outlive one exec
# stream (Freestyle answers VM_NON_RESPONSIVE when the stream drops); the step
# itself keeps running, so poll its status file instead of holding a stream.
vm_step() {
	local vm="$1" step="$2" label="$3" encoded status
	encoded="$(printf 'set -e\n%s\n' "$step" | base64 | tr -d '\n')"
	vm_exec "$vm" "rm -f /tmp/ao-step.rc /tmp/ao-step.log
echo '$encoded' | base64 -d > /tmp/ao-step.sh
nohup sh -c 'sh /tmp/ao-step.sh > /tmp/ao-step.log 2>&1; echo \$? > /tmp/ao-step.rc' >/dev/null 2>&1 &" ||
		return 1
	for _ in $(seq 1 600); do
		sleep 2
		status="$(api POST "/v5/vms/$vm/exec-await" '{"command":"cat /tmp/ao-step.rc 2>/dev/null || true","linuxUser":"root","timeoutMs":10000}' |
			jq -r '.stdout // ""' 2>/dev/null | tr -d '[:space:]')" || continue
		[[ -n "$status" ]] || continue
		if [[ "$status" != 0 ]]; then
			log "$label exited $status"
			api POST "/v5/vms/$vm/exec-await" '{"command":"tail -40 /tmp/ao-step.log; dmesg 2>/dev/null | tail -5","linuxUser":"root","timeoutMs":10000}' |
				jq -r '.stdout // ""' >&2 || true
			return 1
		fi
		return 0
	done
	log "$label did not finish in 20 minutes"
	return 1
}

# run_steps prints each RUN instruction of a Dockerfile, NUL-separated. Anything
# besides FROM and RUN (ENV, COPY, USER, ...) cannot be replayed faithfully in a
# VM, so it fails the bake rather than silently diverging from the Dockerfile.
run_steps() {
	python3 - "$1" <<'PY'
import sys

lines, current = [], ""
for raw in open(sys.argv[1]):
    line = raw.rstrip("\n")
    if not current and (not line.strip() or line.lstrip().startswith("#")):
        continue
    if current and line.lstrip().startswith("#"):
        continue
    # Docker drops the escaping backslash and the newline, nothing else.
    if line.endswith("\\"):
        current += line[:-1]
        continue
    lines.append(current + line)
    current = ""
if current:
    lines.append(current)
for instruction in lines:
    keyword, _, body = instruction.strip().partition(" ")
    keyword = keyword.upper()
    if keyword == "FROM":
        continue
    if keyword != "RUN":
        sys.exit(f"{sys.argv[1]}: unsupported instruction {keyword} for a Freestyle bake")
    sys.stdout.write(body.strip() + "\0")
PY
}

# Extract the exact worker binaries the control plane serves (amd64: Freestyle
# VMs are x86-64) and stage them in S3 for the VM to fetch.
log "extracting ao-worker and ao from $CP_IMAGE"
# A production promote has not pulled the image; log in to its ECR registry.
registry="${CP_IMAGE%%/*}"
if [[ "$registry" == *.dkr.ecr.*.amazonaws.com ]]; then
	aws_cli ecr get-login-password | docker login --username AWS --password-stdin "$registry" >/dev/null
fi
# The control-plane image carries the amd64 worker at /ao-worker whatever its
# own platform, so a single-platform local build (arm64 on a Mac) works too.
container="$(docker create --platform linux/amd64 "$CP_IMAGE" 2>/dev/null || docker create "$CP_IMAGE")"
docker cp "$container:/ao-worker" "$workdir/ao-worker" >/dev/null
docker cp "$container:/ao" "$workdir/ao" >/dev/null
docker rm "$container" >/dev/null
worker_sha="$(shasum -a 256 "$workdir/ao-worker" | cut -d' ' -f1)"
helper_sha="$(shasum -a 256 "$workdir/ao" | cut -d' ' -f1)"
# Keys are content-addressed, so an object already there is this exact binary.
stage() {
	local file="$1" key="$2"
	if aws_cli s3api head-object --bucket "$ARTIFACTS_BUCKET" --key "$key" >/dev/null 2>&1; then
		return 0
	fi
	aws_cli s3 cp --only-show-errors "$file" "s3://$ARTIFACTS_BUCKET/$key"
}
stage "$workdir/ao-worker" "worker/$worker_sha/ao-worker"
stage "$workdir/ao" "worker/$helper_sha/ao"
worker_url="$(aws_cli s3 presign --expires-in 3600 "s3://$ARTIFACTS_BUCKET/worker/$worker_sha/ao-worker")"
helper_url="$(aws_cli s3 presign --expires-in 3600 "s3://$ARTIFACTS_BUCKET/worker/$helper_sha/ao")"
log "baking ao-worker ${worker_sha:0:12} and ao ${helper_sha:0:12}"

# Ubuntu's apt timers can hold the dpkg lock right after boot, and would keep
# running inside every session; stop and disable them before provisioning.
quiesce_apt='systemctl stop apt-daily.timer apt-daily-upgrade.timer unattended-upgrades.service apt-daily.service apt-daily-upgrade.service 2>/dev/null || true
systemctl disable apt-daily.timer apt-daily-upgrade.timer unattended-upgrades.service 2>/dev/null || true
for _ in $(seq 1 120); do pgrep -x "apt|apt-get|dpkg|unattended-upgr" >/dev/null || exit 0; sleep 1; done
echo "apt is still busy" >&2; exit 1'

# Freestyle's Ubuntu base ships its own toolchain: nvm's Node 24 with claude,
# codex, opencode and bun, plus a Python CLI set, all linked into
# /usr/local/bin ahead of /usr/bin on PATH. Left in place, the image steps
# would install into that Node (whose npm 11 also skips install scripts) and
# sessions would run Freestyle's agent builds instead of the pinned ones. Strip
# it so the VM matches the sandbox image; /opt/freestyle itself stays, since
# the platform may rely on it.
strip_base='set -e
for link in /usr/local/bin/*; do
	target="$(readlink "$link" || true)"
	case "$target" in
	/usr/local/nvm/* | /opt/freestyle/python/*) rm -f "$link" ;;
	esac
done
rm -rf /usr/local/nvm /etc/profile.d/nvm.sh'

# Freestyle-only addition to the image toolchain: dtach lets the worker keep
# the coding agent (and everything it started) alive across the worker restart
# a wake from pause performs. Without it the worker launches the agent directly.
freestyle_extras='set -e
apt-get update
apt-get install --yes --no-install-recommends dtach
dtach --help >/dev/null 2>&1 || command -v dtach'

bake_worker="set -e
curl --fail --location --silent --show-error -o /usr/local/bin/ao-worker '$worker_url'
echo '$worker_sha  /usr/local/bin/ao-worker' | sha256sum -c -
curl --fail --location --silent --show-error -o /usr/local/bin/ao '$helper_url'
echo '$helper_sha  /usr/local/bin/ao' | sha256sum -c -
chmod 0755 /usr/local/bin/ao-worker /usr/local/bin/ao"

# finalize prepares what every session start would otherwise do itself, so the
# launch exec only starts the worker: the log file exists, and the workspace
# already belongs to ao-worker (no recursive chown at boot).
finalize='set -e
id -u ao-worker >/dev/null
install -o ao-worker -g ao-worker -m 0644 /dev/null /var/log/ao-worker.log
chown -R ao-worker:ao-worker /workspace
apt-get clean
rm -rf /var/lib/apt/lists/* /root/.npm /tmp/*
sync'

bake_harness() {
	local harness="$1" layer="sandbox-image/harness/$1.Dockerfile" view state="" step index=0
	# Runs in a background subshell. builder_vm is deliberately not local: the
	# EXIT trap fires after this function returns, when its locals are gone.
	builder_vm=""
	trap 'delete_vm "$builder_vm"' EXIT
	local vm
	[[ -f "$layer" ]] || die "missing $layer"
	local check
	case "$harness" in
	claude-code) check='claude --version && test -x "$(command -v claude-agent-acp)"' ;;
	codex) check='codex --version' ;;
	cursor) check='cursor-agent --version' ;;
	*) die "unknown harness $harness" ;;
	esac

	view="$(api POST /v5/vms "$(jq -n --arg snapshot "$BASE_SNAPSHOT" --arg harness "$harness" '{
		snapshotId: $snapshot,
		metadata: {"ao.purpose": "harness-snapshot", "ao.harness": $harness},
		firewall: {rules: [{action: "allow", source: {}, destination: {public: true}}]}
	}')")"
	vm="$(jq -r '.id // empty' <<<"$view")"
	[[ -n "$vm" ]] || die "Freestyle returned no VM id for $harness"
	builder_vm="$vm"
	log "[$harness] builder VM $vm from $BASE_SNAPSHOT"
	for _ in $(seq 1 60); do
		state="$(api GET "/v5/vms/$vm" | jq -r '.state // empty')"
		[[ "$state" == running ]] && break
		sleep 1
	done
	[[ "$state" == running ]] || die "[$harness] VM $vm never reached running (state=$state)"

	vm_exec "$vm" "$quiesce_apt" || die "[$harness] could not quiesce apt"
	vm_exec "$vm" "$strip_base" || die "[$harness] could not strip the base toolchain"
	while IFS= read -r -d '' step; do
		index=$((index + 1))
		log "[$harness] base step $index"
		vm_step "$vm" "$step" "[$harness] base step $index" || die "[$harness] base step $index failed"
	done < <(run_steps "$BASE_DOCKERFILE")
	index=0
	while IFS= read -r -d '' step; do
		index=$((index + 1))
		log "[$harness] harness step $index"
		vm_step "$vm" "$step" "[$harness] harness step $index" || die "[$harness] harness step $index failed"
	done < <(run_steps "$layer")
	vm_step "$vm" "$freestyle_extras" "[$harness] freestyle extras" || die "[$harness] freestyle extras failed"
	vm_step "$vm" "$bake_worker" "[$harness] worker bake" || die "[$harness] worker bake failed"
	vm_exec "$vm" "$finalize" || die "[$harness] finalize failed"
	# Run each binary once so its pages sit in the memory the snapshot captures:
	# the first launch in a session then reads them from memory, not disk.
	# The toolchain must be the image's: Node from /usr/bin, not a leftover.
	vm_exec "$vm" "set -e; test \"\$(command -v node)\" = /usr/bin/node; node --version | grep -q '^v22\\.'
command -v dtach >/dev/null
$check; git --version >/dev/null; gh --version >/dev/null; cat /usr/local/bin/ao-worker /usr/local/bin/ao >/dev/null" ||
		die "[$harness] harness check failed"

	local slug="ao-${harness}-${worker_sha:0:12}-$(date -u +%Y%m%d%H%M%S)"
	local snapshot
	snapshot="$(api POST "/v5/vms/$vm/snapshot" "$(jq -n --arg slug "$slug" '{slug: $slug}')" | jq -r '.snapshotId // empty')"
	[[ -n "$snapshot" ]] || die "[$harness] snapshot returned no id"
	log "[$harness] snapshot $snapshot ($slug)"
	printf '%s\t%s\n' "$harness" "$snapshot" >"$workdir/snapshot-$harness"
	# The EXIT trap covers failures; bash does not reliably run it when a
	# background job ends normally, so delete the builder explicitly too.
	delete_vm "$builder_vm"
	builder_vm=""
}

# The harnesses are independent builder VMs: bake them in parallel.
IFS=',' read -ra harness_list <<<"$HARNESSES"
pids=()
for harness in "${harness_list[@]}"; do
	bake_harness "$harness" &
	pids+=($!)
done
failed=0
for pid in "${pids[@]}"; do
	wait "$pid" || failed=1
done
[[ "$failed" == 0 ]] || die "one or more harness bakes failed"

snapshot_map="$(cat "$workdir"/snapshot-* | jq -R -s 'split("\n") | map(select(length > 0) | split("\t") | {(.[0]): .[1]}) | add')"

# Each release bakes a new set, and Freestyle plans cap the snapshot count
# (10 on Free). Keep the two newest per harness: the set just baked and the one
# before it, which sessions started before this release still name for a
# repair (Recreate boots from the session's own snapshot). Project snapshots
# (ao-project-*) are pruned by the control plane, not here.
prune_harness_snapshots() {
	local listing harness stale
	listing="$(api GET /v5/snapshots)" || { log "could not list snapshots; skipping prune"; return 0; }
	for harness in "${harness_list[@]}"; do
		stale="$(jq -r --arg h "$harness" '
			[.snapshots[]
			 | select((.slug // "") | test("^ao-" + $h + "-[0-9a-f]{12}-[0-9]{14}$"))]
			| sort_by(.createdAt) | reverse | .[2:][] | .id' <<<"$listing")"
		for id in $stale; do
			if api DELETE "/v5/snapshots/$id" >/dev/null; then
				log "[$harness] pruned snapshot $id"
			fi
		done
	done
}
prune_harness_snapshots

if [[ "$UPDATE_SECRET" == 1 ]]; then
	current="$(aws_cli secretsmanager get-secret-value --secret-id "$FREESTYLE_SECRET_ID" --query SecretString --output text)"
	updated="$(jq --arg map "$(jq -c . <<<"$snapshot_map")" \
		'.snapshot_by_harness = $map | .default_snapshot = ($map | fromjson)["claude-code"] // .default_snapshot' <<<"$current")"
	aws_cli secretsmanager put-secret-value --secret-id "$FREESTYLE_SECRET_ID" --secret-string "$updated" >/dev/null
	unset current updated
	log "updated $FREESTYLE_SECRET_ID"
fi
jq -c . <<<"$snapshot_map"
