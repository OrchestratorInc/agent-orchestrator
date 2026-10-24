#!/usr/bin/env bash
set -euo pipefail

REGION="${AWS_REGION:-eu-north-1}"
STAGING_CLUSTER="${AO_CLOUD_STAGING_CLUSTER:-ao-cloud-staging}"
STAGING_SERVICE="${AO_CLOUD_STAGING_SERVICE:-ao-cloud-staging-api}"
STAGING_ROLLBACK_ALARM="${AO_CLOUD_STAGING_ROLLBACK_ALARM:-ao-cloud-staging-target-5xx}"
PRODUCTION_CLUSTER="${AO_CLOUD_PRODUCTION_CLUSTER:-ao-cloud-production}"
PRODUCTION_SERVICE="${AO_CLOUD_PRODUCTION_SERVICE:-ao-cloud-production-api}"
PRODUCTION_API_FAMILY="${AO_CLOUD_PRODUCTION_API_FAMILY:-ao-cloud-production-api}"
PRODUCTION_MIGRATION_FAMILY="${AO_CLOUD_PRODUCTION_MIGRATION_FAMILY:-ao-cloud-production-migrate}"
PRODUCTION_ALB="${AO_CLOUD_PRODUCTION_ALB:-ao-cloud-production-public}"
PRODUCTION_TARGET_GROUP="${AO_CLOUD_PRODUCTION_TARGET_GROUP:-ao-cloud-production-public-cp}"
PRODUCTION_TASK_SECURITY_GROUP="${AO_CLOUD_PRODUCTION_TASK_SECURITY_GROUP:-ao-cloud-production-task-sg}"
ROLLBACK_ALARM="${AO_CLOUD_PRODUCTION_ROLLBACK_ALARM:-ao-cloud-production-target-5xx}"
RUNTIME_DATABASE_USER="${AO_CLOUD_RUNTIME_DATABASE_USER:-ao_cloud_app}"
NODEOPS_SECRET_ID="${AO_CLOUD_NODEOPS_SECRET_ID:-ao-cloud/production/nodeops}"
CODER_SECRET_ID="${AO_CLOUD_CODER_SECRET_ID:-ao-cloud/production/coder}"
FREESTYLE_SECRET_ID="${AO_CLOUD_FREESTYLE_SECRET_ID:-ao-cloud/production/freestyle}"
WORKER_SECRET_ID="${AO_CLOUD_WORKER_SECRET_ID:-ao-cloud/production/worker}"
# Stripe billing is optional: without this secret production runs with billing
# off.
STRIPE_SECRET_ID="${AO_CLOUD_STRIPE_SECRET_ID:-ao-cloud/production/stripe}"
SUPPORT_SECRET_ID="${AO_CLOUD_SUPPORT_SECRET_ID:-ao-cloud/production/support}"
# Where a Freestyle bake stages the worker binaries for its builder VMs.
ARTIFACTS_BUCKET="${AO_CLOUD_PRODUCTION_ARTIFACTS_BUCKET:-ao-cloud-production-artifacts}"
# SKIP_FREESTYLE_BAKE=1 reuses the snapshots already recorded in the Freestyle
# secret, so a Freestyle outage cannot block a promotion. New sessions then
# fetch the current worker from the control plane before starting.
SKIP_FREESTYLE_BAKE="${SKIP_FREESTYLE_BAKE:-}"

# shellcheck source=lib/deploy-common.sh
source ./scripts/lib/deploy-common.sh

# --rolling-200-100 rolls production with maximumPercent=200 and
# minimumHealthyPercent=100 instead of the default 100/0, so the old task keeps
# serving until the new one is healthy (see deployment_configuration).
ROLLING=false
RELEASE_ARG=""
for arg in "$@"; do
	case "$arg" in
	--rolling-200-100) ROLLING=true ;;
	-*)
		echo "Unknown option $arg (usage: promote-production.sh [--rolling-200-100] [release])." >&2
		exit 1
		;;
	*)
		if [[ -n "$RELEASE_ARG" ]]; then
			echo "Pass at most one release." >&2
			exit 1
		fi
		RELEASE_ARG="$arg"
		;;
	esac
done

AWS_OPTIONS=(--region "$REGION")
if [[ -n "${AWS_PROFILE:-}" ]]; then
	AWS_OPTIONS+=(--profile "$AWS_PROFILE")
fi

aws_cli() {
	aws "${AWS_OPTIONS[@]}" "$@"
}

# providers_has reports whether $1 is one of the comma-separated PROVIDERS.
providers_has() {
	[[ ",${PROVIDERS}," == *",$1,"* ]]
}

if [[ "${AO_CLOUD_APPROVE_PRODUCTION:-}" != "1" ]]; then
	echo "Set AO_CLOUD_APPROVE_PRODUCTION=1 to approve production promotion." >&2
	exit 1
fi

staging_task="$(
	aws_cli ecs describe-services \
		--cluster "$STAGING_CLUSTER" \
		--services "$STAGING_SERVICE" \
		--query 'services[0].taskDefinition' \
		--output text
)"
staging_source="$(
	aws_cli ecs describe-task-definition \
		--task-definition "$staging_task" \
		--include TAGS
)"
staging_release="$(
	SOURCE="$staging_source" python3 - <<'PY'
import json
import os

source = json.loads(os.environ["SOURCE"])
container = next(
    item
    for item in source["taskDefinition"]["containerDefinitions"]
    if item["name"] == "control-plane"
)
environment = {item["name"]: item["value"] for item in container["environment"]}
print(environment["AO_CLOUD_RELEASE"])
PY
)"
# The providers the healthy staging task serves: production may serve any
# subset of them, never one staging has not exercised.
staging_providers="$(
	SOURCE="$staging_source" python3 - <<'PY'
import json
import os

source = json.loads(os.environ["SOURCE"])
container = next(
    item
    for item in source["taskDefinition"]["containerDefinitions"]
    if item["name"] == "control-plane"
)
environment = {item["name"]: item["value"] for item in container["environment"]}
print(environment.get("AO_CLOUD_SANDBOX_PROVIDERS", environment["AO_CLOUD_SANDBOX_PROVIDER"]))
PY
)"
# Production declares its own offering in scripts/offering/production.env; a
# value set in the promoting shell wins. Only the secrets of providers
# production actually serves are required.
load_offering ./scripts/offering/production.env
# Artifacts, like a provider, reach production only after staging serves them.
staging_artifacts="$(
	SOURCE="$staging_source" python3 - <<'PY'
import json
import os

source = json.loads(os.environ["SOURCE"])
container = next(
    item
    for item in source["taskDefinition"]["containerDefinitions"]
    if item["name"] == "control-plane"
)
environment = {item["name"]: item["value"] for item in container["environment"]}
print(environment.get("AO_CLOUD_ARTIFACTS", "0"))
PY
)"
if [[ "$AO_CLOUD_ARTIFACTS" == 1 && "$staging_artifacts" != 1 ]]; then
	echo "AO_CLOUD_ARTIFACTS=1 in production needs staging to serve artifacts first (staging has ${staging_artifacts})." >&2
	exit 1
fi
SANDBOX_PROVIDER="$AO_CLOUD_SANDBOX_PROVIDER"
PROVIDERS="$AO_CLOUD_SANDBOX_PROVIDERS"
if [[ "$SANDBOX_PROVIDER" != "nodeops" && "$SANDBOX_PROVIDER" != "coder" && "$SANDBOX_PROVIDER" != "freestyle" ]]; then
	echo "AO_CLOUD_SANDBOX_PROVIDER must be nodeops, coder, or freestyle." >&2
	exit 1
fi
IFS=',' read -ra _providers_list <<<"$PROVIDERS"
for _provider in "${_providers_list[@]}"; do
	if [[ "$_provider" != "nodeops" && "$_provider" != "coder" && "$_provider" != "freestyle" ]]; then
		echo "AO_CLOUD_SANDBOX_PROVIDERS entries must each be nodeops, coder, or freestyle, got: $_provider" >&2
		exit 1
	fi
done
if ! providers_has "$SANDBOX_PROVIDER"; then
	echo "AO_CLOUD_SANDBOX_PROVIDER ($SANDBOX_PROVIDER) must be one of AO_CLOUD_SANDBOX_PROVIDERS ($PROVIDERS)." >&2
	exit 1
fi
./scripts/validate-hosted-settings.py \
	--offering \
	--environment production \
	--staging-providers "$staging_providers" \
	--providers "$PROVIDERS" \
	--provider "$SANDBOX_PROVIDER" \
	--capability-gated-providers "$AO_CLOUD_CAPABILITY_GATED_PROVIDERS" \
	--default-org-capabilities "$AO_CLOUD_DEFAULT_ORG_CAPABILITIES" \
	--default-org-billing "$AO_CLOUD_DEFAULT_ORG_BILLING" \
	--paywalled-features "$AO_CLOUD_PAYWALLED_FEATURES" \
	--idle-pause-threshold "$AO_CLOUD_IDLE_PAUSE_THRESHOLD" \
	--freestyle-idle-pause-threshold "$AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD" \
	--artifacts "$AO_CLOUD_ARTIFACTS" \
	--artifact-bucket "$AO_CLOUD_ARTIFACT_BUCKET" \
	--artifact-region "$AO_CLOUD_ARTIFACT_REGION" \
	--coder-readable-names "$AO_CLOUD_CODER_READABLE_NAMES" \
	--coder-worker-working-folder "$AO_CLOUD_CODER_WORKER_WORKING_FOLDER"
echo "Offering: providers=${PROVIDERS} (staging: ${staging_providers}) default=${SANDBOX_PROVIDER} gated=${AO_CLOUD_CAPABILITY_GATED_PROVIDERS:-none} default_org_capabilities=${AO_CLOUD_DEFAULT_ORG_CAPABILITIES:-none} default_org_billing=${AO_CLOUD_DEFAULT_ORG_BILLING} paywalled=${AO_CLOUD_PAYWALLED_FEATURES:-none} artifacts=${AO_CLOUD_ARTIFACTS}" >&2
check_artifact_bucket "$PRODUCTION_CLUSTER" "$PRODUCTION_SERVICE" || exit 1
release="${RELEASE_ARG:-$staging_release}"
if [[ "$release" != "$staging_release" ]]; then
	echo "Release ${release} is not the healthy staging release ${staging_release}." >&2
	exit 1
fi
./scripts/verify-ecs-service.py \
	--region "$REGION" \
	--cluster "$STAGING_CLUSTER" \
	--service "$STAGING_SERVICE" \
	--alarm "$STAGING_ROLLBACK_ALARM" \
	--expected-task-definition "$staging_task" >/dev/null
./scripts/verify-ecs-service.py \
	--region "$REGION" \
	--cluster "$PRODUCTION_CLUSTER" \
	--service "$PRODUCTION_SERVICE" \
	--alarm "$ROLLBACK_ALARM" >/dev/null
# The task definition running before this promotion. Its Freestyle snapshots
# stay protected from the post-promotion prune.
previous_task="$(
	aws_cli ecs describe-services \
		--cluster "$PRODUCTION_CLUSTER" \
		--services "$PRODUCTION_SERVICE" \
		--query 'services[0].taskDefinition' \
		--output text
)"

control_image="$(
	SOURCE="$staging_source" python3 - <<'PY'
import json
import os

source = json.loads(os.environ["SOURCE"])
container = next(
    item
    for item in source["taskDefinition"]["containerDefinitions"]
    if item["name"] == "control-plane"
)
print(container["image"])
PY
)"
worker_image="$(
	SOURCE="$staging_source" python3 - <<'PY'
import json
import os

source = json.loads(os.environ["SOURCE"])
tags = {item["key"]: item["value"] for item in source.get("tags", [])}
print(tags.get("WorkerImage", ""))
PY
)"
if [[ "$control_image" != *@sha256:* || "$worker_image" != *@sha256:* ]]; then
	echo "Staging artifacts are not pinned to immutable image digests." >&2
	exit 1
fi

./scripts/verify-ecs-service.py \
	--region "$REGION" \
	--cluster "$STAGING_CLUSTER" \
	--service "$STAGING_SERVICE" \
	--alarm "$STAGING_ROLLBACK_ALARM" \
	--expected-task-definition "$staging_task" \
	--expected-control-image "$control_image" \
	--expected-worker-image "$worker_image" >/dev/null

# Same worker CVE allowlist as deploy-staging.sh. Inspector flags unpatched
# Debian packages pulled in by git; a zero HIGH/CRITICAL gate would block every
# promote until Debian ships fixes. New HIGH/CRITICAL IDs still fail the gate.
# The glibc, libssh2, and sqlite3 additions are classified by Debian as minor
# no-DSA issues in Trixie; patched packages exist only in unstable as of
# 2026-08-31.
verify_scan() {
	local image="$1"
	local repository_uri="${image%@sha256:*}"
	local repository="${repository_uri##*/}"
	local digest="sha256:${image##*@sha256:}"
	local scan
	scan="$(
		aws_cli ecr describe-image-scan-findings \
			--repository-name "$repository" \
			--image-id "imageDigest=${digest}"
	)"
	# CVE-2026-14456 is a scanner false positive for Debian's OpenSSL 3.0:
	# its QUIC listener was introduced in OpenSSL 3.5.
	if ! SCAN="$scan" ALLOWLIST="${AO_CLOUD_SCAN_CVE_ALLOWLIST:-CVE-2026-57432 CVE-2026-45186 CVE-2026-12087 CVE-2025-15661 CVE-2026-58051 CVE-2026-7017 CVE-2026-48962 CVE-2026-57433 CVE-2026-66032 CVE-2026-48961 CVE-2026-48959 CVE-2026-66034 CVE-2026-58050 CVE-2026-13221 CVE-2026-14456 CVE-2026-66046 CVE-2026-63076 CVE-2026-53615 CVE-2026-54874 CVE-2026-63072 CVE-2026-8927 CVE-2026-8924 CVE-2026-8286 CVE-2026-85091 CVE-2026-93990 CVE-2026-95619 CVE-2026-95210 CVE-2026-88647 CVE-2026-67693 CVE-2026-95184 CVE-2026-95209 CVE-2026-107161 CVE-2026-5435 CVE-2026-19499}" python3 - <<'PY'
import json
import os
import sys

scan = json.loads(os.environ["SCAN"])
if scan["imageScanStatus"]["status"] != "COMPLETE":
    raise SystemExit(1)
allow = set(os.environ.get("ALLOWLIST", "").split())
findings = scan["imageScanFindings"].get("findings") or []
blocking = [
    f
    for f in findings
    if f.get("severity") in ("CRITICAL", "HIGH") and f.get("name") not in allow
]
for f in blocking:
    sys.stderr.write(f"blocking {f.get('severity')} {f.get('name')}\n")
sys.exit(1 if blocking else 0)
PY
	then
		echo "The staging artifact ${repository} does not have a clean completed ECR scan." >&2
		exit 1
	fi
}

verify_scan "$control_image"
verify_scan "$worker_image"
control_image_digest="sha256:${control_image##*@sha256:}"
worker_image_digest="sha256:${worker_image##*@sha256:}"

secret_arn() {
	aws_cli secretsmanager describe-secret \
		--secret-id "$1" \
		--query ARN \
		--output text
}

worker_secret_arn="$(secret_arn "$WORKER_SECRET_ID")"
provider_secret_arn="$(secret_arn ao-cloud/production/provider-secret-key)"
worker_settings="$(
	aws_cli secretsmanager get-secret-value \
		--secret-id "$WORKER_SECRET_ID" \
		--query SecretString \
		--output text
)"
# Resolve, validate, and later plumb the secrets for every provider production
# serves, so a multi-provider promote keeps both providers' secrets.
if providers_has nodeops; then
	nodeops_secret_arn="$(secret_arn "$NODEOPS_SECRET_ID")"
	nodeops_settings="$(
		aws_cli secretsmanager get-secret-value \
			--secret-id "$NODEOPS_SECRET_ID" \
			--query SecretString \
			--output text
	)"
	./scripts/validate-hosted-settings.py \
		--nodeops <(printf '%s' "$nodeops_settings") \
		--worker <(printf '%s' "$worker_settings")
	rootfs_by_harness="$(jq -r '.rootfs_by_harness // "{}"' <<<"$nodeops_settings")"
	unset nodeops_settings
fi
if providers_has coder; then
	coder_secret_arn="$(secret_arn "$CODER_SECRET_ID")"
	coder_settings="$(
		aws_cli secretsmanager get-secret-value \
			--secret-id "$CODER_SECRET_ID" \
			--query SecretString \
			--output text
	)"
	./scripts/validate-hosted-settings.py \
		--coder <(printf '%s' "$coder_settings") \
		--worker <(printf '%s' "$worker_settings")
	unset coder_settings
fi
if providers_has freestyle; then
	freestyle_secret_arn="$(secret_arn "$FREESTYLE_SECRET_ID")"
	freestyle_settings="$(
		aws_cli secretsmanager get-secret-value \
			--secret-id "$FREESTYLE_SECRET_ID" \
			--query SecretString \
			--output text
	)"
	./scripts/validate-hosted-settings.py \
		--freestyle <(printf '%s' "$freestyle_settings") \
		--worker <(printf '%s' "$worker_settings")
	# Snapshot ids are not credentials; plaintext keeps a malformed optional key
	# from blocking container start, as for NodeOps rootfs_by_harness.
	snapshot_by_harness="$(jq -r '.snapshot_by_harness' <<<"$freestyle_settings")"
	unset freestyle_settings
fi
unset worker_settings

# The operator support lookup token is optional: without it the endpoint is off.
support_secret_arn="$(optional_secret_arn "$SUPPORT_SECRET_ID")"
# Only a missing secret turns billing off; any other lookup error fails here.
stripe_secret_arn="$(optional_secret_arn "$STRIPE_SECRET_ID")"
if [[ -n "$stripe_secret_arn" ]]; then
	stripe_settings="$(
		aws_cli secretsmanager get-secret-value \
			--secret-id "$STRIPE_SECRET_ID" \
			--query SecretString \
			--output text
	)"
	./scripts/validate-hosted-settings.py \
		--stripe <(printf '%s' "$stripe_settings") \
		--environment production
	# Price ids are not credentials; the key and webhook secret stay secrets.
	stripe_price_ids="$(jq -c '.price_ids | if type == "string" then fromjson else . end' <<<"$stripe_settings")"
	stripe_portal_configuration="$(jq -r '.portal_configuration // ""' <<<"$stripe_settings")"
	# Organizations created before billing went live stay exempt.
	stripe_exempt_before="$(jq -r '.exempt_orgs_created_before // ""' <<<"$stripe_settings")"
	unset stripe_settings
	echo "Stripe billing: enabled from $STRIPE_SECRET_ID" >&2
else
	echo "Stripe billing: off ($STRIPE_SECRET_ID not found)" >&2
fi

# Freestyle boots sessions from snapshots with ao-worker baked in, so a release
# that changes the worker must rebake them; otherwise every new session first
# downloads the current worker from the control plane. Bake from the exact
# control-plane digest into the production bucket, record the new snapshot ids
# in the Freestyle secret, and give this release's task those ids directly. Old
# snapshots are pruned only after the new task is healthy.
freestyle_baked=false
if providers_has freestyle && [[ "$SKIP_FREESTYLE_BAKE" == 1 ]]; then
	echo "Freestyle bake: skipped (SKIP_FREESTYLE_BAKE=1); reusing the snapshots in $FREESTYLE_SECRET_ID." >&2
elif providers_has freestyle; then
	if ! aws_cli s3api head-bucket --bucket "$ARTIFACTS_BUCKET" >/dev/null; then
		echo "Freestyle bake needs the production artifacts bucket ${ARTIFACTS_BUCKET} (AO_CLOUD_PRODUCTION_ARTIFACTS_BUCKET)." >&2
		exit 1
	fi
	snapshot_by_harness="$(
		AWS_PROFILE="${AWS_PROFILE:-}" \
			AWS_REGION="$REGION" \
			AO_CLOUD_CP_IMAGE="$control_image" \
			AO_CLOUD_FREESTYLE_SECRET_ID="$FREESTYLE_SECRET_ID" \
			AO_CLOUD_ARTIFACTS_BUCKET="$ARTIFACTS_BUCKET" \
			AO_CLOUD_FREESTYLE_UPDATE_SECRET=1 \
			AO_CLOUD_FREESTYLE_PRUNE=0 \
			./scripts/publish-freestyle-snapshot.sh | tail -n 1
	)"
	jq -e 'type == "object"' <<<"$snapshot_by_harness" >/dev/null ||
		{ echo "Freestyle snapshot bake did not return a snapshot map." >&2; exit 1; }
	freestyle_baked=true
fi

aws_cli iam get-role --role-name ao-cloud-production-execution-role >/dev/null
aws_cli iam get-role --role-name ao-cloud-production-task-role >/dev/null
aws_cli secretsmanager describe-secret \
	--secret-id ao-cloud/production/database-url >/dev/null
aws_cli secretsmanager describe-secret \
	--secret-id ao-cloud/production/migration-database-url >/dev/null
aws_cli secretsmanager describe-secret \
	--secret-id ao-cloud/production/workos >/dev/null
github_secret_arn="$(secret_arn ao-cloud/production/github)"
broker_secret_arn="$(secret_arn "${AO_CLOUD_REPOSITORY_BROKER_SECRET_ID:-ao-cloud/repository-broker}")"

register_api_task() {
	local source payload
	source="$(
		aws_cli ecs describe-task-definition \
			--task-definition "$PRODUCTION_API_FAMILY" \
			--include TAGS
	)"
	local render_args=(
		--family "$PRODUCTION_API_FAMILY"
		--container control-plane
		--image "$control_image"
		--worker-image "$worker_image"
		--release "$release"
		--environment production
		--log-group /ao-cloud/production/control-plane
		--region "$REGION"
		--sandbox-provider "$SANDBOX_PROVIDER"
		--sandbox-providers "$PROVIDERS"
		--set-environment AO_CLOUD_PUBLIC_URL=https://api.aoagents.dev
		--set-environment AO_CLOUD_TERMINAL_STREAM=1
		--set-environment AO_CLOUD_TERMINAL_RELAY=1
		--set-secret "AO_CLOUD_GITHUB_APP_ID=${github_secret_arn}:app_id::"
		--set-secret "AO_CLOUD_GITHUB_APP_SLUG=${github_secret_arn}:app_slug::"
		--set-secret "AO_CLOUD_GITHUB_CLIENT_ID=${github_secret_arn}:client_id::"
		--set-secret "AO_CLOUD_GITHUB_CLIENT_SECRET=${github_secret_arn}:client_secret::"
		--set-secret "AO_CLOUD_GITHUB_PRIVATE_KEY=${github_secret_arn}:private_key::"
		--set-secret "AO_CLOUD_GITHUB_WEBHOOK_SECRET=${github_secret_arn}:webhook_secret::"
		--set-secret "AO_CLOUD_GITHUB_STATE_KEY=${github_secret_arn}:state_key::"
		--set-secret "AO_CLOUD_REPOSITORY_BROKER_TOKEN=${broker_secret_arn}:auth_token::"
		--set-secret "AO_CLOUD_PROVIDER_SECRET_KEY=${provider_secret_arn}"
		--set-secret "AO_CLOUD_WORKER_SIGNING_KEY=${worker_secret_arn}:signing_key::"
		--set-secret "AO_CLOUD_MAX_ACTIVE_SANDBOXES_PER_ORG=${worker_secret_arn}:max_active_sandboxes_per_org::"
		--set-secret "AO_CLOUD_SANDBOX_RECONCILE_INTERVAL=${worker_secret_arn}:sandbox_reconcile_interval::"
		--set-secret "AO_CLOUD_SANDBOX_STARTUP_TIMEOUT=${worker_secret_arn}:sandbox_startup_timeout::"
		--set-secret "AO_CLOUD_WORKER_HEARTBEAT_TIMEOUT=${worker_secret_arn}:worker_heartbeat_timeout::"
		--set-environment "AO_CLOUD_CAPABILITY_GATED_PROVIDERS=${AO_CLOUD_CAPABILITY_GATED_PROVIDERS}"
		--set-environment "AO_CLOUD_DEFAULT_ORG_CAPABILITIES=${AO_CLOUD_DEFAULT_ORG_CAPABILITIES}"
		--set-environment "AO_CLOUD_DEFAULT_ORG_BILLING=${AO_CLOUD_DEFAULT_ORG_BILLING}"
		--set-environment "AO_CLOUD_PAYWALLED_FEATURES=${AO_CLOUD_PAYWALLED_FEATURES}"
		--set-environment "AO_CLOUD_IDLE_PAUSE_THRESHOLD=${AO_CLOUD_IDLE_PAUSE_THRESHOLD}"
		--set-environment "AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD=${AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD}"
		--set-environment "AO_CLOUD_ARTIFACTS=${AO_CLOUD_ARTIFACTS}"
		--set-environment "AO_CLOUD_ARTIFACT_BUCKET=${AO_CLOUD_ARTIFACT_BUCKET}"
		--set-environment "AO_CLOUD_ARTIFACT_REGION=${AO_CLOUD_ARTIFACT_REGION}"
		--set-environment "AO_CLOUD_CODER_READABLE_NAMES=${AO_CLOUD_CODER_READABLE_NAMES}"
		--set-environment "AO_CLOUD_CODER_WORKER_WORKING_FOLDER=${AO_CLOUD_CODER_WORKER_WORKING_FOLDER}"
	)
	if providers_has coder; then
		render_args+=(
			--set-secret "AO_CLOUD_CODER_URL=${coder_secret_arn}:url::"
			--set-secret "AO_CLOUD_CODER_TOKEN=${coder_secret_arn}:token::"
			--set-secret "AO_CLOUD_CODER_OWNER=${coder_secret_arn}:owner::"
			--set-secret "AO_CLOUD_CODER_TEMPLATE_ID=${coder_secret_arn}:template_id::"
			--set-secret "AO_CLOUD_CODER_TEMPLATE_IDS=${coder_secret_arn}:template_ids::"
			--set-secret "AO_CLOUD_CODER_AGENT_NAME=${coder_secret_arn}:agent_name::"
			--set-secret "AO_CLOUD_CODER_PARAMETERS_JSON=${coder_secret_arn}:parameters_json::"
			--set-secret "AO_CLOUD_CODER_DURABLE_ROOT=${coder_secret_arn}:durable_root::"
			--set-secret "AO_CLOUD_CODER_WORKER_TOKEN_TTL=${coder_secret_arn}:worker_token_ttl::"
		)
	fi
	if providers_has nodeops; then
		render_args+=(
			--set-environment "AO_CLOUD_NODEOPS_ROOTFS_BY_HARNESS=${rootfs_by_harness}"
			--set-secret "AO_CLOUD_NODEOPS_BASE_URL=${nodeops_secret_arn}:base_url::"
			--set-secret "AO_CLOUD_NODEOPS_API_KEY=${nodeops_secret_arn}:api_key::"
			--set-secret "AO_CLOUD_NODEOPS_DEFAULT_SHAPE=${nodeops_secret_arn}:default_shape::"
			--set-secret "AO_CLOUD_NODEOPS_DEFAULT_ROOTFS=${nodeops_secret_arn}:default_rootfs::"
			--set-secret "AO_CLOUD_NODEOPS_INGRESS=${nodeops_secret_arn}:ingress::"
			--set-secret "AO_CLOUD_NODEOPS_SSH_KEY_PATH=${nodeops_secret_arn}:ssh_key_path::"
			--set-secret "AO_CLOUD_NODEOPS_REGION=${nodeops_secret_arn}:region::"
			--set-secret "AO_CLOUD_NODEOPS_WORKER_TOKEN_TTL=${nodeops_secret_arn}:worker_token_ttl::"
		)
	fi
	if providers_has freestyle; then
		render_args+=(
			--set-environment "AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS=${snapshot_by_harness}"
			--set-secret "AO_CLOUD_FREESTYLE_API_KEY=${freestyle_secret_arn}:api_key::"
			--set-secret "AO_CLOUD_FREESTYLE_DEFAULT_SNAPSHOT=${freestyle_secret_arn}:default_snapshot::"
			--set-secret "AO_CLOUD_FREESTYLE_WORKER_TOKEN_TTL=${freestyle_secret_arn}:worker_token_ttl::"
		)
	fi
	if [[ -n "$support_secret_arn" ]]; then
		render_args+=(--set-secret "AO_CLOUD_SUPPORT_TOKEN=${support_secret_arn}:token::")
	fi
	if [[ -n "$stripe_secret_arn" ]]; then
		render_args+=(
			--set-environment "AO_CLOUD_STRIPE_PRICE_IDS=${stripe_price_ids}"
			--set-environment "AO_CLOUD_STRIPE_PORTAL_CONFIGURATION=${stripe_portal_configuration}"
			--set-environment "AO_CLOUD_BILLING_EXEMPT_ORGS_CREATED_BEFORE=${stripe_exempt_before}"
			--set-secret "AO_CLOUD_STRIPE_SECRET_KEY=${stripe_secret_arn}:secret_key::"
			--set-secret "AO_CLOUD_STRIPE_WEBHOOK_SECRET=${stripe_secret_arn}:webhook_secret::"
		)
	fi
	payload="$(printf '%s' "$source" | ./scripts/render-task-definition.py "${render_args[@]}")"
	aws_cli ecs register-task-definition \
		--cli-input-json "$payload" \
		--query 'taskDefinition.taskDefinitionArn' \
		--output text
}

register_migration_task() {
	local source payload
	source="$(
		aws_cli ecs describe-task-definition \
			--task-definition "$PRODUCTION_MIGRATION_FAMILY" \
			--include TAGS
	)"
	payload="$(
		printf '%s' "$source" |
			./scripts/render-task-definition.py \
				--family "$PRODUCTION_MIGRATION_FAMILY" \
				--container migration \
				--image "$control_image" \
				--release "$release" \
				--environment production \
				--log-group /ao-cloud/production/control-plane \
				--region "$REGION" \
				--runtime-database-user "$RUNTIME_DATABASE_USER"
	)"
	aws_cli ecs register-task-definition \
		--cli-input-json "$payload" \
		--query 'taskDefinition.taskDefinitionArn' \
		--output text
}

api_task="$(register_api_task)"
migration_task="$(register_migration_task)"
load_balancer="$(
	aws_cli elbv2 describe-load-balancers \
		--names "$PRODUCTION_ALB" \
		--query 'LoadBalancers[0]' \
		--output json
)"
subnets="$(
	LOAD_BALANCER="$load_balancer" python3 - <<'PY'
import json
import os

load_balancer = json.loads(os.environ["LOAD_BALANCER"])
print(",".join(zone["SubnetId"] for zone in load_balancer["AvailabilityZones"]))
PY
)"
vpc_id="$(
	LOAD_BALANCER="$load_balancer" python3 - <<'PY'
import json
import os

print(json.loads(os.environ["LOAD_BALANCER"])["VpcId"])
PY
)"
task_security_group="$(
	aws_cli ec2 describe-security-groups \
		--filters \
			"Name=vpc-id,Values=${vpc_id}" \
			"Name=group-name,Values=${PRODUCTION_TASK_SECURITY_GROUP}" \
		--query 'SecurityGroups[0].GroupId' \
		--output text
)"
network_configuration="awsvpcConfiguration={subnets=[${subnets}],securityGroups=[${task_security_group}],assignPublicIp=ENABLED}"

migration_result="$(
	aws_cli ecs run-task \
		--cluster "$PRODUCTION_CLUSTER" \
		--launch-type FARGATE \
		--platform-version LATEST \
		--task-definition "$migration_task" \
		--network-configuration "$network_configuration" \
		--started-by "promote-${release:0:27}" \
		--tags \
			key=Project,value=ao-cloud \
			key=Environment,value=production \
			"key=Release,value=${release}"
)"
migration_arn="$(
	MIGRATION_RESULT="$migration_result" python3 - <<'PY'
import json
import os

result = json.loads(os.environ["MIGRATION_RESULT"])
if result.get("failures") or not result.get("tasks"):
    raise SystemExit("ECS refused to start the production migration task")
print(result["tasks"][0]["taskArn"])
PY
)"
cleanup_migration() {
	if [[ -n "${migration_arn:-}" && "${migration_complete:-false}" != "true" ]]; then
		aws_cli ecs stop-task \
			--cluster "$PRODUCTION_CLUSTER" \
			--task "$migration_arn" \
			--reason "AO Cloud promotion interrupted before migration completion" \
			>/dev/null 2>&1 || true
		echo "Stopped incomplete production migration task ${migration_arn}." >&2
	fi
}
trap cleanup_migration EXIT
aws_cli ecs wait tasks-stopped \
	--cluster "$PRODUCTION_CLUSTER" \
	--tasks "$migration_arn"
migration_exit="$(
	aws_cli ecs describe-tasks \
		--cluster "$PRODUCTION_CLUSTER" \
		--tasks "$migration_arn" \
		--query 'tasks[0].containers[0].exitCode' \
		--output text
)"
if [[ "$migration_exit" != "0" ]]; then
	aws_cli ecs describe-tasks \
		--cluster "$PRODUCTION_CLUSTER" \
		--tasks "$migration_arn" \
		--query 'tasks[0].{reason:stoppedReason,containerReason:containers[0].reason}' \
		--output json >&2
	exit 1
fi
migration_complete=true

target_group="$(
	aws_cli elbv2 describe-target-groups \
		--names "$PRODUCTION_TARGET_GROUP" \
		--query 'TargetGroups[0].TargetGroupArn' \
		--output text
)"
deployment_configuration="$(deployment_configuration "$ROLLBACK_ALARM" "$ROLLING")"
if [[ "$ROLLING" == true ]]; then
	echo "Rolling production 200/100: the old task drains for the target group's deregistration delay." >&2
fi
service_status="$(
	aws_cli ecs describe-services \
		--cluster "$PRODUCTION_CLUSTER" \
		--services "$PRODUCTION_SERVICE" \
		--query 'services[0].status' \
		--output text 2>/dev/null || true
)"
if [[ "$service_status" == "ACTIVE" ]]; then
	aws_cli ecs update-service \
		--cluster "$PRODUCTION_CLUSTER" \
		--service "$PRODUCTION_SERVICE" \
		--task-definition "$api_task" \
		--desired-count 1 \
		--health-check-grace-period-seconds 60 \
		--availability-zone-rebalancing DISABLED \
		--deployment-configuration "$deployment_configuration" \
		>/dev/null
else
	aws_cli ecs create-service \
		--cluster "$PRODUCTION_CLUSTER" \
		--service-name "$PRODUCTION_SERVICE" \
		--task-definition "$api_task" \
		--desired-count 1 \
		--launch-type FARGATE \
		--platform-version LATEST \
		--network-configuration "$network_configuration" \
		--load-balancers \
			"targetGroupArn=${target_group},containerName=control-plane,containerPort=8080" \
		--deployment-configuration "$deployment_configuration" \
		--availability-zone-rebalancing DISABLED \
		--health-check-grace-period-seconds 60 \
		--enable-ecs-managed-tags \
		--propagate-tags TASK_DEFINITION \
		--tags \
			key=Project,value=ao-cloud \
			key=Environment,value=production \
		>/dev/null
fi

aws_cli application-autoscaling register-scalable-target \
	--service-namespace ecs \
	--scalable-dimension ecs:service:DesiredCount \
	--resource-id "service/${PRODUCTION_CLUSTER}/${PRODUCTION_SERVICE}" \
	--min-capacity 1 \
	--max-capacity 1 \
	>/dev/null
aws_cli application-autoscaling delete-scaling-policy \
	--service-namespace ecs \
	--scalable-dimension ecs:service:DesiredCount \
	--resource-id "service/${PRODUCTION_CLUSTER}/${PRODUCTION_SERVICE}" \
	--policy-name ao-cloud-production-cpu \
	>/dev/null 2>&1 || true

aws_cli ecs wait services-stable \
	--cluster "$PRODUCTION_CLUSTER" \
	--services "$PRODUCTION_SERVICE"
deployed_task="$(
	aws_cli ecs describe-services \
		--cluster "$PRODUCTION_CLUSTER" \
		--services "$PRODUCTION_SERVICE" \
		--query 'services[0].taskDefinition' \
		--output text
)"
if [[ "$deployed_task" != "$api_task" ]]; then
	echo "Production rolled back instead of deploying ${api_task}." >&2
	exit 1
fi

production_source="$(
	aws_cli ecs describe-task-definition \
		--task-definition "$deployed_task"
)"
if ! STAGING_IMAGE="$control_image" RELEASE="$release" SOURCE="$production_source" python3 - <<'PY'
import json
import os

source = json.loads(os.environ["SOURCE"])
container = next(
    item
    for item in source["taskDefinition"]["containerDefinitions"]
    if item["name"] == "control-plane"
)
environment = {item["name"]: item["value"] for item in container["environment"]}
if container["image"] != os.environ["STAGING_IMAGE"]:
    raise SystemExit("production image differs from staging")
if environment.get("AO_CLOUD_RELEASE") != os.environ["RELEASE"]:
    raise SystemExit("production release differs from staging")
PY
then
	exit 1
fi

verification_error=""
verify_args=()
verify_attempts=18
if [[ "$ROLLING" == true ]]; then
	# The replaced task's target drains for the deregistration delay.
	verify_args+=(--allow-draining-targets)
	verify_attempts=36
fi
for _ in $(seq 1 "$verify_attempts"); do
	if verification_error="$(
		./scripts/verify-ecs-service.py \
			--region "$REGION" \
			--cluster "$PRODUCTION_CLUSTER" \
			--service "$PRODUCTION_SERVICE" \
			--alarm "$ROLLBACK_ALARM" \
			--expected-task-definition "$api_task" \
			--expected-control-image "$control_image" \
			--expected-worker-image "$worker_image" \
			${verify_args[@]+"${verify_args[@]}"} 2>&1
	)"; then
		verification_error=""
		break
	fi
	sleep 10
done
if [[ -n "$verification_error" ]]; then
	echo "$verification_error" >&2
	exit 1
fi
trap - EXIT

# Only now, with the new task healthy, delete old harness snapshots. The task
# that ran before this promotion and the one now running stay protected, as does
# whatever staging runs. A failed prune never fails the promotion.
if [[ "$freestyle_baked" == true ]]; then
	AWS_PROFILE="${AWS_PROFILE:-}" \
		AWS_REGION="$REGION" \
		AO_CLOUD_FREESTYLE_SECRET_ID="$FREESTYLE_SECRET_ID" \
		AO_CLOUD_FREESTYLE_PRUNE_ONLY=1 \
		AO_CLOUD_FREESTYLE_PROTECT_TASK_DEFINITIONS="$previous_task $api_task" \
		./scripts/publish-freestyle-snapshot.sh ||
		echo "warning: Freestyle snapshot prune failed; old snapshots remain." >&2
fi

printf 'Promoted release %s\nControl-plane digest: %s\nWorker digest: %s\nTask definition: %s\n' \
	"$release" \
	"$control_image_digest" \
	"$worker_image_digest" \
	"$api_task"
