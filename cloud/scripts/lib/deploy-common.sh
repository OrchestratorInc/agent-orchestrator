#!/usr/bin/env bash
# Shared by deploy-staging.sh and promote-production.sh: the offering settings,
# the optional-secret lookup, and the ECS deployment configuration.

# The settings a scripts/offering/<environment>.env file may declare.
OFFERING_SETTINGS=(
	AO_CLOUD_SANDBOX_PROVIDERS
	AO_CLOUD_SANDBOX_PROVIDER
	AO_CLOUD_CAPABILITY_GATED_PROVIDERS
	AO_CLOUD_DEFAULT_ORG_CAPABILITIES
	AO_CLOUD_DEFAULT_ORG_BILLING
	AO_CLOUD_PAYWALLED_FEATURES
	AO_CLOUD_IDLE_PAUSE_THRESHOLD
	AO_CLOUD_FREESTYLE_IDLE_PAUSE_THRESHOLD
	AO_CLOUD_ARTIFACTS
	AO_CLOUD_ARTIFACT_BUCKET
	AO_CLOUD_ARTIFACT_REGION
	AO_CLOUD_CODER_READABLE_NAMES
	AO_CLOUD_CODER_WORKER_WORKING_FOLDER
)

# load_offering FILE sets each setting FILE declares unless the caller's
# environment already set it (an explicit empty value counts as set). Values are
# taken literally: the file is parsed, never sourced. An unknown or malformed
# line fails, and so does a setting no one declared.
load_offering() {
	local file="$1" line name value known setting
	[[ -f "$file" ]] || { echo "Missing offering file $file." >&2; return 1; }
	while IFS= read -r line || [[ -n "$line" ]]; do
		[[ -z "${line//[[:space:]]/}" || "$line" =~ ^[[:space:]]*# ]] && continue
		if [[ ! "$line" =~ ^([A-Z0-9_]+)=(.*)$ ]]; then
			echo "$file: malformed line: $line" >&2
			return 1
		fi
		name="${BASH_REMATCH[1]}"
		value="${BASH_REMATCH[2]}"
		known=false
		for setting in "${OFFERING_SETTINGS[@]}"; do
			[[ "$setting" == "$name" ]] && known=true
		done
		if [[ "$known" != true ]]; then
			echo "$file: unknown setting $name" >&2
			return 1
		fi
		if [[ -z "${!name+set}" ]]; then
			printf -v "$name" '%s' "$value"
			export "${name?}"
		fi
	done <"$file"
	for setting in "${OFFERING_SETTINGS[@]}"; do
		if [[ -z "${!setting+set}" ]]; then
			echo "$file does not declare $setting." >&2
			return 1
		fi
	done
}

# check_artifact_bucket CLUSTER SERVICE fails, when AO_CLOUD_ARTIFACTS=1,
# unless the artifact bucket exists with versioning enabled and the service's
# task role may use it. The control plane makes no AWS call at startup, so
# without this a missing bucket or permission would show up only as failed
# uploads. Expects the caller's aws_cli.
check_artifact_bucket() {
	[[ "$AO_CLOUD_ARTIFACTS" == 1 ]] || return 0
	local cluster="$1" service="$2"
	if ! aws_cli s3api head-bucket --bucket "$AO_CLOUD_ARTIFACT_BUCKET" >/dev/null; then
		echo "AO_CLOUD_ARTIFACTS=1 needs the artifact bucket ${AO_CLOUD_ARTIFACT_BUCKET}; create it first (docs/deployment.md, Session artifacts)." >&2
		return 1
	fi
	# The deploy scripts call this as `check_artifact_bucket ... || exit 1`, so
	# errexit does not apply inside it: every AWS call checks its own status.
	local versioning
	if ! versioning="$(aws_cli s3api get-bucket-versioning --bucket "$AO_CLOUD_ARTIFACT_BUCKET" --query Status --output text)"; then
		echo "Could not read the versioning of the artifact bucket ${AO_CLOUD_ARTIFACT_BUCKET}." >&2
		return 1
	fi
	if [[ "$versioning" != "Enabled" ]]; then
		echo "The artifact bucket ${AO_CLOUD_ARTIFACT_BUCKET} must have versioning enabled (got: ${versioning})." >&2
		return 1
	fi
	local task_definition role_arn bucket_arn denied_objects denied_bucket denied
	if ! task_definition="$(aws_cli ecs describe-services --cluster "$cluster" --services "$service" \
		--query 'services[0].taskDefinition' --output text)"; then
		echo "Could not read the task definition of ${service}." >&2
		return 1
	fi
	if ! role_arn="$(aws_cli ecs describe-task-definition --task-definition "$task_definition" \
		--query 'taskDefinition.taskRoleArn' --output text)"; then
		echo "Could not read the task role of ${task_definition}." >&2
		return 1
	fi
	if [[ -z "$role_arn" || "$role_arn" == "None" ]]; then
		echo "AO_CLOUD_ARTIFACTS=1 needs a task role on ${service}; its task definition has none." >&2
		return 1
	fi
	bucket_arn="arn:aws:s3:::${AO_CLOUD_ARTIFACT_BUCKET}"
	if ! denied_objects="$(aws_cli iam simulate-principal-policy --policy-source-arn "$role_arn" \
		--action-names s3:PutObject s3:GetObject s3:GetObjectVersion s3:DeleteObjectVersion \
		--resource-arns "${bucket_arn}/orgs/deploy-check/sessions/deploy-check/file" \
		--query "EvaluationResults[?EvalDecision!='allowed'].EvalActionName" --output text)"; then
		echo "Could not check the task role ${role_arn}'s object access to ${AO_CLOUD_ARTIFACT_BUCKET}: iam simulate-principal-policy failed." >&2
		return 1
	fi
	if ! denied_bucket="$(aws_cli iam simulate-principal-policy --policy-source-arn "$role_arn" \
		--action-names s3:ListBucket s3:ListBucketVersions \
		--resource-arns "$bucket_arn" \
		--query "EvaluationResults[?EvalDecision!='allowed'].EvalActionName" --output text)"; then
		echo "Could not check the task role ${role_arn}'s listing of ${AO_CLOUD_ARTIFACT_BUCKET}: iam simulate-principal-policy failed." >&2
		return 1
	fi
	denied="$(echo "$denied_objects $denied_bucket" | tr -s '[:space:]' ' ' | sed 's/^ *//; s/ *$//')"
	if [[ -n "$denied" ]]; then
		echo "The task role ${role_arn} may not use ${AO_CLOUD_ARTIFACT_BUCKET}: ${denied} denied (docs/deployment.md, Session artifacts)." >&2
		return 1
	fi
}

# optional_secret_arn ID prints the secret's ARN, or nothing when the secret
# does not exist. Any other failure (expired credentials, throttling, a missing
# permission) fails, rather than quietly turning the optional feature off.
# Expects the caller's aws_cli.
optional_secret_arn() {
	local errors arn status=0
	errors="$(mktemp)"
	arn="$(aws_cli secretsmanager describe-secret --secret-id "$1" --query ARN --output text 2>"$errors")" || status=$?
	if [[ "$status" == 0 ]]; then
		rm -f "$errors"
		printf '%s' "$arn"
		return 0
	fi
	if grep -q ResourceNotFoundException "$errors"; then
		rm -f "$errors"
		return 0
	fi
	cat "$errors" >&2
	rm -f "$errors"
	echo "Could not look up $1." >&2
	return 1
}

# deployment_configuration ALARM ROLLING prints the ECS deployment
# configuration. The default (ROLLING=false) replaces the single task in place,
# 100/0: no second task ever runs, but the service has no healthy target for a
# few minutes. ROLLING=true is 200/100: the new task must pass health checks
# before the old one is drained, so there is no outage, at the cost of two
# control planes briefly running side by side and the old target draining for
# the target group's deregistration delay (300s in production).
deployment_configuration() {
	local alarm="$1" rolling="$2" maximum=100 minimum=0
	if [[ "$rolling" == true ]]; then
		maximum=200
		minimum=100
	fi
	printf '{"maximumPercent":%s,"minimumHealthyPercent":%s,"deploymentCircuitBreaker":{"enable":true,"rollback":true},"alarms":{"alarmNames":["%s"],"enable":true,"rollback":true}}' \
		"$maximum" "$minimum" "$alarm"
}
