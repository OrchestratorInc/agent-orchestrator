#!/usr/bin/env bash
set -euo pipefail

REGION="${AWS_REGION:-eu-north-1}"
CLUSTER="${AO_CLOUD_ECS_CLUSTER:-ao-cloud-staging}"
SERVICE="${AO_CLOUD_ECS_SERVICE:-ao-cloud-staging-api}"
CONTROL_REPOSITORY="${AO_CLOUD_ECR_REPOSITORY:-ao-cloud-control-plane}"
WORKER_REPOSITORY="${AO_CLOUD_WORKER_ECR_REPOSITORY:-ao-cloud-worker}"
API_FAMILY="${AO_CLOUD_API_TASK_FAMILY:-ao-cloud-staging-api}"
MIGRATION_FAMILY="${AO_CLOUD_MIGRATION_TASK_FAMILY:-ao-cloud-staging-migrate}"
ROLLBACK_ALARM="${AO_CLOUD_ROLLBACK_ALARM:-ao-cloud-staging-target-5xx}"
RUNTIME_DATABASE_USER="${AO_CLOUD_RUNTIME_DATABASE_USER:-ao_cloud_app}"

# shellcheck source=lib/deploy-common.sh
source ./scripts/lib/deploy-common.sh

ROLLING=false
RELEASE_ARG=""
for arg in "$@"; do
	case "$arg" in
	--rolling-200-100) ROLLING=true ;;
	-*)
		echo "Unknown option $arg (usage: deploy-staging.sh [--rolling-200-100] [release])." >&2
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

# The offering (providers, capability gate, default org capabilities and
# billing, paywalled features, idle pause) comes from scripts/offering/staging.env
# unless the deploying shell sets a value. Every served provider's secrets are
# plumbed and preserved, so a deploy never drops an inactive provider's
# credentials.
load_offering ./scripts/offering/staging.env
SANDBOX_PROVIDER="$AO_CLOUD_SANDBOX_PROVIDER"
PROVIDERS="$AO_CLOUD_SANDBOX_PROVIDERS"
# SKIP_FREESTYLE_BAKE=1 reuses the snapshots already recorded in the Freestyle
# secret, so a Freestyle outage cannot block a release. New sessions then fetch
# the current worker from the control plane before starting.
SKIP_FREESTYLE_BAKE="${SKIP_FREESTYLE_BAKE:-}"
# SKIP_CODER_WORKSPACE_PUBLISH=1 keeps the published Coder workspace image (for
# a host that cannot build it, or a Coder registry outage). Workers self-update
# at boot, so a stale baked worker only costs the first start a download.
SKIP_CODER_WORKSPACE_PUBLISH="${SKIP_CODER_WORKSPACE_PUBLISH:-}"
ARTIFACTS_BUCKET="${AO_CLOUD_ARTIFACTS_BUCKET:-ao-cloud-staging-artifacts}"
NODEOPS_SECRET_ID="${AO_CLOUD_NODEOPS_SECRET_ID:-ao-cloud/staging/nodeops}"
CODER_SECRET_ID="${AO_CLOUD_CODER_SECRET_ID:-ao-cloud/staging/coder}"
FREESTYLE_SECRET_ID="${AO_CLOUD_FREESTYLE_SECRET_ID:-ao-cloud/staging/freestyle}"
WORKER_SECRET_ID="${AO_CLOUD_WORKER_SECRET_ID:-ao-cloud/staging/worker}"
# Stripe billing is optional: without this secret the control plane runs with
# billing off, as before.
STRIPE_SECRET_ID="${AO_CLOUD_STRIPE_SECRET_ID:-ao-cloud/staging/stripe}"
SUPPORT_SECRET_ID="${AO_CLOUD_SUPPORT_SECRET_ID:-ao-cloud/staging/support}"
HEAD_SHA="$(git rev-parse HEAD)"
RELEASE="${RELEASE_ARG:-$HEAD_SHA}"
IMAGE_TAG="${RELEASE//+/-}-linux-amd64"

# CVE allowlist shared with promote-production.sh. These CRITICAL/HIGH findings
# are unpatched 2026 CVEs in base OS packages (perl, libssh2, expat, openssl, util-linux, curl, zlib) that git
# pulls into the worker image; no fix exists in Debian 12 or 13 yet. The worker
# runs inside an ephemeral, single-tenant, isolated provider workspace that
# already executes untrusted agent code. Keep this list in sync with production
# promote. Remove entries as Debian ships fixes; the worker stage's apt-get
# upgrade then clears them on rebuild. CVE-2026-14456 is a scanner false
# positive for Debian's OpenSSL 3.0: its QUIC listener was introduced in 3.5.
# The curl (CVE-2026-8927 cross-proxy Digest leak, CVE-2026-8924 trailing-dot
# super cookie, CVE-2026-8286 STARTTLS reuse) and zlib (CVE-2026-85091 non-blocking
# gzwrite overflow) findings are base OS packages with no Debian fix yet, and the
# worker does not exercise their vulnerable paths (no proxy/cookie/STARTTLS curl
# use; git uses zlib inflate/deflate, not the gz file API). CVE-2026-8286 and
# CVE-2026-85091 are fixed upstream, pending a Debian backport. The expat
# (CVE-2026-93990 UTF-16 surrogate validation) and libstdc++ (CVE-2026-95619
# aligned operator new overflow) findings have no Debian 13 fix yet; the worker
# parses no UTF-16 XML and makes no attacker-sized aligned allocations.
# The gnutls28 findings (CVE-2026-95210, CVE-2026-88647, CVE-2026-67693:
# certificate and hostname checks that accept a crafted certificate;
# CVE-2026-95184, CVE-2026-95209: rejecting valid ones) and cyrus-sasl2
# (CVE-2026-107161, DIGEST-MD5 client overflow) have no Debian 13 fix yet. Only
# git's HTTPS transport and curl link gnutls (ao-worker, node and the agents use
# their own TLS); abusing it needs an on-path attacker holding a CA-signed
# certificate for the target. The DIGEST-MD5 plugin (libsasl2-modules) is not
# installed, so the SASL overflow is unreachable. This image runs only for the
# docker provider; Coder and Freestyle sessions boot Ubuntu images instead.
# The glibc findings (CVE-2026-5435: the deprecated ns_printrr, ns_printrrf and
# fp_nquery resolver printers overrun on TSIG records; CVE-2026-19499: strfmon
# and strfmon_l overrun with right-justified width padding) have no Debian 13
# fix yet. Nothing in the worker image prints DNS records with the old resolver
# helpers or formats money with strfmon.
SCAN_CVE_ALLOWLIST="${AO_CLOUD_SCAN_CVE_ALLOWLIST:-CVE-2026-57432 CVE-2026-45186 CVE-2026-12087 CVE-2025-15661 CVE-2026-58051 CVE-2026-7017 CVE-2026-48962 CVE-2026-57433 CVE-2026-66032 CVE-2026-48961 CVE-2026-48959 CVE-2026-66034 CVE-2026-58050 CVE-2026-13221 CVE-2026-14456 CVE-2026-66046 CVE-2026-63076 CVE-2026-53615 CVE-2026-54874 CVE-2026-63072 CVE-2026-8927 CVE-2026-8924 CVE-2026-8286 CVE-2026-85091 CVE-2026-93990 CVE-2026-95619 CVE-2026-95210 CVE-2026-88647 CVE-2026-67693 CVE-2026-95184 CVE-2026-95209 CVE-2026-107161 CVE-2026-5435 CVE-2026-19499}"

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

if [[ -n "$(git status --porcelain)" ]]; then
	echo "Refusing to deploy a dirty working tree." >&2
	exit 1
fi
if [[ ! "$RELEASE" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]{0,199}$ ]]; then
	echo "Release must be a Git SHA or release tag." >&2
	exit 1
fi
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
if ! resolved_release="$(git rev-parse "${RELEASE}^{commit}" 2>/dev/null)" ||
	[[ "$resolved_release" != "$HEAD_SHA" ]]; then
	echo "Release must resolve to the current commit ${HEAD_SHA}." >&2
	exit 1
fi
./scripts/validate-hosted-settings.py \
	--offering \
	--environment staging \
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
echo "Offering: providers=${PROVIDERS} default=${SANDBOX_PROVIDER} gated=${AO_CLOUD_CAPABILITY_GATED_PROVIDERS:-none} default_org_capabilities=${AO_CLOUD_DEFAULT_ORG_CAPABILITIES:-none} default_org_billing=${AO_CLOUD_DEFAULT_ORG_BILLING} paywalled=${AO_CLOUD_PAYWALLED_FEATURES:-none} artifacts=${AO_CLOUD_ARTIFACTS}" >&2
check_artifact_bucket "$CLUSTER" "$SERVICE" || exit 1

./scripts/verify-ecs-service.py \
	--region "$REGION" \
	--cluster "$CLUSTER" \
	--service "$SERVICE" \
	--alarm "$ROLLBACK_ALARM" >/dev/null
# The task definition running before this deploy. Its Freestyle snapshots stay
# protected from the post-deploy prune.
previous_task="$(
	aws_cli ecs describe-services \
		--cluster "$CLUSTER" \
		--services "$SERVICE" \
		--query 'services[0].taskDefinition' \
		--output text
)"

secret_arn() {
	aws_cli secretsmanager describe-secret \
		--secret-id "$1" \
		--query ARN \
		--output text
}

provider_secret_arn="$(secret_arn "${AO_CLOUD_PROVIDER_SECRET_ID:-ao-cloud/staging/provider-secret-key}")"
worker_secret_arn="$(secret_arn "$WORKER_SECRET_ID")"
broker_secret_arn="$(secret_arn "${AO_CLOUD_REPOSITORY_BROKER_SECRET_ID:-ao-cloud/repository-broker}")"
worker_settings="$(
	aws_cli secretsmanager get-secret-value \
		--secret-id "$WORKER_SECRET_ID" \
		--query SecretString \
		--output text
)"
# Resolve, validate, and later plumb the secrets for every provider this control
# plane serves. A multi-provider deploy (nodeops,coder) keeps both providers'
# secrets instead of pruning the inactive one.
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
	# Optional per-harness template mapping. It remains plaintext so a missing
	# optional JSON key cannot prevent the ECS container from starting.
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
		--environment staging
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

publish_image() {
	local repository="$1"
	local target="$2"
	PUBLISHED_URI="$(
		aws_cli ecr describe-repositories \
			--repository-names "$repository" \
			--query 'repositories[0].repositoryUri' \
			--output text
	)"
	local registry="${PUBLISHED_URI%%/*}"
	aws_cli ecr get-login-password |
		docker login --username AWS --password-stdin "$registry" >/dev/null
	if ! aws_cli ecr describe-images \
		--repository-name "$repository" \
		--image-ids "imageTag=${IMAGE_TAG}" >/dev/null 2>&1; then
		docker build \
			--platform linux/amd64 \
			--provenance=false \
			--target "$target" \
			--tag "${PUBLISHED_URI}:${IMAGE_TAG}" \
			.
		docker push "${PUBLISHED_URI}:${IMAGE_TAG}"
	fi
	PUBLISHED_DIGEST="$(
		aws_cli ecr describe-images \
			--repository-name "$repository" \
			--image-ids "imageTag=${IMAGE_TAG}" \
			--query 'imageDetails[0].imageDigest' \
			--output text
	)"
	PUBLISHED_IMAGE="${PUBLISHED_URI}@${PUBLISHED_DIGEST}"
}

scan_image() {
	local repository="$1"
	local digest="$2"
	local scan_status=""
	aws_cli ecr start-image-scan \
		--repository-name "$repository" \
		--image-id "imageDigest=${digest}" >/dev/null 2>&1 || true
	for _ in $(seq 1 60); do
		scan_status="$(
			aws_cli ecr describe-image-scan-findings \
				--repository-name "$repository" \
				--image-id "imageDigest=${digest}" \
				--query 'imageScanStatus.status' \
				--output text 2>/dev/null || true
		)"
		case "$scan_status" in
		COMPLETE) break ;;
		FAILED) echo "ECR image scan failed for ${repository}." >&2; exit 1 ;;
		esac
		sleep 2
	done
	if [[ "$scan_status" != "COMPLETE" ]]; then
		echo "ECR image scan did not complete for ${repository}." >&2
		exit 1
	fi

	local findings
	findings="$(
		aws_cli ecr describe-image-scan-findings \
			--repository-name "$repository" \
			--image-id "imageDigest=${digest}" \
			--query 'imageScanFindings.findings[?severity==`CRITICAL`||severity==`HIGH`].{name:name,severity:severity}' \
			--output json
	)"
	if ! FINDINGS="$findings" ALLOWLIST="$SCAN_CVE_ALLOWLIST" python3 - <<'PY'
import json
import os
import sys

findings = json.loads(os.environ.get("FINDINGS") or "[]")
allow = set(os.environ.get("ALLOWLIST", "").split())
blocking = [f for f in findings if f.get("name") not in allow]
for f in blocking:
    sys.stderr.write(f"blocking {f.get('severity')} {f.get('name')}\n")
sys.exit(1 if blocking else 0)
PY
	then
		echo "ECR scan found non-allowlisted critical or high vulnerabilities in ${repository}." >&2
		exit 1
	fi
}

publish_image "$CONTROL_REPOSITORY" control-plane
control_image="$PUBLISHED_IMAGE"
control_image_digest="$PUBLISHED_DIGEST"
publish_image "$WORKER_REPOSITORY" worker
worker_image="$PUBLISHED_IMAGE"
worker_image_digest="$PUBLISHED_DIGEST"
docker pull "$control_image" >/dev/null
docker pull "$worker_image" >/dev/null
./scripts/verify-image-contract.sh "$control_image" "$worker_image"
scan_image "$CONTROL_REPOSITORY" "$control_image_digest"
scan_image "$WORKER_REPOSITORY" "$worker_image_digest"

# Coder bakes ao-worker into its workspace image, so a release that changes the
# worker must rebake that image or every spawn and resume falls back to the slow
# PTY upload (internal/sandbox/coder/client.go preinstalledCheck). Rebuild and
# publish it here, before the rollout, from the exact control-plane digest so the
# baked binaries are byte-identical to what the reconciler advertises
# (AO_WORKER_EXPECTED_SHA256). The nodeops path is unaffected: its template is
# published out of band by scripts/publish-nodeops-template.sh.
if providers_has coder && [[ "$SKIP_CODER_WORKSPACE_PUBLISH" == 1 ]]; then
	echo "Coder workspace image: skipped (SKIP_CODER_WORKSPACE_PUBLISH=1); workers self-update at boot." >&2
elif providers_has coder; then
	AWS_REGION="$REGION" \
		AO_CLOUD_CP_IMAGE="$control_image" \
		AO_CLOUD_CODER_SECRET_ID="$CODER_SECRET_ID" \
		AO_CLOUD_CODER_WORKSPACE_IMAGE_TAG="${IMAGE_TAG}" \
		./scripts/publish-coder-workspace.sh
fi

# Freestyle boots sessions from snapshots with ao-worker baked in, so a release
# that changes the worker must rebake them; otherwise every new session first
# downloads the current worker from the control plane. Bake from the exact
# control-plane digest, record the new snapshot ids in the Freestyle secret, and
# give this release's task those ids directly. Old snapshots are pruned only
# after the new task is healthy (see the end of this script).
freestyle_baked=false
if providers_has freestyle && [[ "$SKIP_FREESTYLE_BAKE" == 1 ]]; then
	echo "Freestyle bake: skipped (SKIP_FREESTYLE_BAKE=1); reusing the snapshots in $FREESTYLE_SECRET_ID." >&2
elif providers_has freestyle; then
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

register_task_definition() {
	local family="$1"
	local container_name="$2"
	local source payload
	source="$(aws_cli ecs describe-task-definition --task-definition "$family" --include TAGS)"
	local render_args=(
		--family "$family"
		--container "$container_name"
		--image "$control_image"
		--release "$RELEASE"
		--environment staging
		--log-group /ao-cloud/staging/control-plane
		--region "$REGION"
		--sandbox-provider "$SANDBOX_PROVIDER"
		--sandbox-providers "$PROVIDERS"
	)
	if [[ "$container_name" == "control-plane" ]]; then
		render_args+=(
			--worker-image "$worker_image"
			--set-environment "AO_CLOUD_PUBLIC_URL=${AO_CLOUD_PUBLIC_URL:-https://staging-api.aoagents.dev}"
			--set-environment AO_CLOUD_TERMINAL_STREAM=1
			--set-environment AO_CLOUD_TERMINAL_RELAY=1
			--set-environment AO_CLOUD_REPOSITORY_BROKER_URL=https://api.aoagents.dev
			--set-environment AO_CLOUD_ALLOW_ANONYMOUS_GITHUB_CHECKOUT=true
			--set-secret "AO_CLOUD_PROVIDER_SECRET_KEY=${provider_secret_arn}"
			--set-secret "AO_CLOUD_REPOSITORY_BROKER_TOKEN=${broker_secret_arn}:auth_token::"
			--set-secret "AO_CLOUD_ENV_CONTROL_TOKEN=${broker_secret_arn}:staging_control_token::"
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
		if providers_has freestyle; then
			render_args+=(
				--set-environment "AO_CLOUD_FREESTYLE_SNAPSHOT_BY_HARNESS=${snapshot_by_harness}"
				--set-secret "AO_CLOUD_FREESTYLE_API_KEY=${freestyle_secret_arn}:api_key::"
				--set-secret "AO_CLOUD_FREESTYLE_DEFAULT_SNAPSHOT=${freestyle_secret_arn}:default_snapshot::"
				--set-secret "AO_CLOUD_FREESTYLE_WORKER_TOKEN_TTL=${freestyle_secret_arn}:worker_token_ttl::"
			)
		fi
	else
		render_args+=(--runtime-database-user "$RUNTIME_DATABASE_USER")
	fi
	payload="$(printf '%s' "$source" | ./scripts/render-task-definition.py "${render_args[@]}")"
	aws_cli ecs register-task-definition \
		--cli-input-json "$payload" \
		--query 'taskDefinition.taskDefinitionArn' \
		--output text
}

api_task="$(register_task_definition "$API_FAMILY" control-plane)"
migration_task="$(register_task_definition "$MIGRATION_FAMILY" migration)"
network_configuration="$(
	aws_cli ecs describe-services \
		--cluster "$CLUSTER" \
		--services "$SERVICE" \
		--query 'services[0].networkConfiguration' \
		--output json
)"
migration_result="$(
	aws_cli ecs run-task \
		--cluster "$CLUSTER" \
		--launch-type FARGATE \
		--platform-version LATEST \
		--task-definition "$migration_task" \
		--network-configuration "$network_configuration" \
		--started-by "deploy-${RELEASE:0:28}" \
		--tags \
			key=Project,value=ao-cloud \
			key=Environment,value="${AO_CLOUD_DEPLOY_ENVIRONMENT:-staging}" \
			"key=Release,value=${RELEASE}"
)"
migration_arn="$(
	MIGRATION_RESULT="$migration_result" python3 - <<'PY'
import json
import os

result = json.loads(os.environ["MIGRATION_RESULT"])
if result.get("failures") or not result.get("tasks"):
    raise SystemExit("ECS refused to start the migration task")
print(result["tasks"][0]["taskArn"])
PY
)"
cleanup_migration() {
	if [[ -n "${migration_arn:-}" && "${migration_complete:-false}" != "true" ]]; then
		aws_cli ecs stop-task \
			--cluster "$CLUSTER" \
			--task "$migration_arn" \
			--reason "AO Cloud deployment interrupted before migration completion" \
			>/dev/null 2>&1 || true
		echo "Stopped incomplete migration task ${migration_arn}." >&2
	fi
}
trap cleanup_migration EXIT
aws_cli ecs wait tasks-stopped --cluster "$CLUSTER" --tasks "$migration_arn"
migration_exit="$(
	aws_cli ecs describe-tasks \
		--cluster "$CLUSTER" \
		--tasks "$migration_arn" \
		--query 'tasks[0].containers[0].exitCode' \
		--output text
)"
if [[ "$migration_exit" != "0" ]]; then
	aws_cli ecs describe-tasks \
		--cluster "$CLUSTER" \
		--tasks "$migration_arn" \
		--query 'tasks[0].{reason:stoppedReason,containerReason:containers[0].reason}' \
		--output json >&2
	exit 1
fi
migration_complete=true

aws_cli ecs update-service \
	--cluster "$CLUSTER" \
	--service "$SERVICE" \
	--task-definition "$api_task" \
	--desired-count 1 \
	--health-check-grace-period-seconds 60 \
	--availability-zone-rebalancing DISABLED \
	--deployment-configuration "$(deployment_configuration "$ROLLBACK_ALARM" "$ROLLING")" \
	>/dev/null
aws_cli ecs wait services-stable --cluster "$CLUSTER" --services "$SERVICE"

deployed_task="$(
	aws_cli ecs describe-services \
		--cluster "$CLUSTER" \
		--services "$SERVICE" \
		--query 'services[0].taskDefinition' \
		--output text
)"
if [[ "$deployed_task" != "$api_task" ]]; then
	echo "ECS rolled back instead of deploying ${api_task}." >&2
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
			--cluster "$CLUSTER" \
			--service "$SERVICE" \
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
# that ran before this deploy and the one now running stay protected, as does
# whatever production runs. A failed prune never fails the deploy.
if [[ "$freestyle_baked" == true ]]; then
	AWS_PROFILE="${AWS_PROFILE:-}" \
		AWS_REGION="$REGION" \
		AO_CLOUD_FREESTYLE_SECRET_ID="$FREESTYLE_SECRET_ID" \
		AO_CLOUD_FREESTYLE_PRUNE_ONLY=1 \
		AO_CLOUD_FREESTYLE_PROTECT_TASK_DEFINITIONS="$previous_task $api_task" \
		./scripts/publish-freestyle-snapshot.sh ||
		echo "warning: Freestyle snapshot prune failed; old snapshots remain." >&2
fi

printf 'Deployed release %s\nControl-plane digest: %s\nWorker digest: %s\nTask definition: %s\n' \
	"$RELEASE" \
	"$control_image_digest" \
	"$worker_image_digest" \
	"$api_task"
