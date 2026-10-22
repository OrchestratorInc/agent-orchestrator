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
