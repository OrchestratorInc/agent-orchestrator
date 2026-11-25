#!/usr/bin/env bash
# Fails if this public repository depends on the private AO Cloud control
# plane (OrchestratorInc/ao-control-plane). Everything a user installs must
# build from the code in this repository alone; the control plane is reached
# only through the public contract (contracts/cloud/openapi.yaml).
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

status=0
fail() {
	echo "public boundary: $*" >&2
	status=1
}

# 1. go.work may only use directories that exist in this checkout.
if [[ -f go.work ]]; then
	while read -r dir; do
		[[ -d "$dir" ]] || fail "go.work uses $dir, which is not in this repository"
	done < <(go work edit -json | python3 -c 'import json,sys; [print(u["DiskPath"]) for u in json.load(sys.stdin).get("Use") or []]')
fi

# 2. The control plane lives in its own repository.
if git ls-files --error-unmatch cloud >/dev/null 2>&1 || [[ -n "$(git ls-files cloud/ | head -1)" ]]; then
	fail "cloud/ is tracked; the control plane lives in OrchestratorInc/ao-control-plane"
fi

# 3. No public Go code may import the control plane's module.
if matches="$(git grep -lE '"github\.com/aoagents/agent-orchestrator/cloud(/|")' -- '*.go' || true)" && [[ -n "$matches" ]]; then
	fail "Go files import the control plane module:"$'\n'"$matches"
fi

# 4. No npm script may run something from the control plane's tree.
while IFS= read -r manifest; do
	if python3 - "$manifest" <<'PY'
import json, re, sys
scripts = json.load(open(sys.argv[1])).get("scripts") or {}
bad = [name for name, cmd in scripts.items() if re.search(r'(^|[\s"\'=])(\./|(\.\./)+)?cloud/', cmd)]
if bad:
    print(f"{sys.argv[1]}: scripts {', '.join(bad)} reference cloud/")
    sys.exit(1)
PY
	then :; else status=1; fi
done < <(git ls-files 'package.json' '*/package.json' | grep -v node_modules)

# 5. No tracked file knows the control plane's private internals: settings
#    only it reads, its database roles, its deployment's labels, or a script
#    or checkout of its repository. Code here reaches it through the contract
#    alone; a test that stands it up (such as the session move round trip)
#    lives in its repository and runs this one's daemon from there.
private_names=(
	AO_CLOUD_DEVELOPMENT_ AO_CLOUD_WORKER_SIGNING_KEY AO_CLOUD_PROVIDER_SECRET_KEY AO_CLOUD_DOCKER_
	AO_CLOUD_LOCAL_POSTGRES_ AO_CLOUD_LOCAL_MINIO_ AO_CLOUD_LOCAL_UID AO_CLOUD_LOCAL_GID
	ao_cloud_owner ao_cloud_bootstrap ao_cloud_app
	scripts/lib/docker-local.sh AO_CONTROL_PLANE ao.docker.namespace
)
patterns=()
for name in "${private_names[@]}"; do patterns+=(-e "$name"); done
if matches="$(git grep -lF "${patterns[@]}" -- . ':!scripts/check-public-boundary.sh' ':!scripts/check-public-boundary.test.sh' || true)" && [[ -n "$matches" ]]; then
	fail "files name the control plane's private internals (keep what stands it up in its repository):"$'\n'"$matches"
fi

if [[ "$status" == 0 ]]; then
	echo "public boundary: ok"
fi
exit "$status"
