#!/usr/bin/env bash
set -euo pipefail

# Publish the two AO Dev-kit Coder templates (medium + large) to the Coder
# deployment. Both reuse coder/main.tf and a dev-kit image (coder/devkit.Dockerfile
# = the approved workspace image + plain developer tooling layered on top, so the
# baked ao-worker SHA is unchanged and the reconciler fast path still matches).
# They differ only by the per-workspace memory/CPU set at push time, so the picker
# shows two entries with no size/startup form controls.
#
# The workspace image is distributed via ECR (the Coder host pulls from it, same
# as the default ao-linux-docker template), so this runs from a workstation with
# AWS + docker + the coder CLI; no Coder-host SSH needed.
#
# Run from the cloud/ directory.

export AWS_PROFILE="${AWS_PROFILE:-ao-cloud}"
export AWS_REGION="${AWS_REGION:-eu-north-1}"
CODER_SECRET_ID="${AO_CLOUD_CODER_SECRET_ID:-ao-cloud/production/coder}"
REG="${AO_ECR_REGISTRY:-479575345906.dkr.ecr.eu-north-1.amazonaws.com}"
ECR_REPO="${AO_CODER_WORKSPACE_REPO:-ao-cloud-coder-workspace}"
# Base = the exact image the live default template (ao-linux-docker) runs, so the
# harnesses and the release-matched ao-worker binaries match the deployed control
# plane. Derived at run time from that template unless AO_DEVKIT_BASE_IMAGE is set,
# so this stays correct across control-plane/image rebakes.
BASE_IMAGE="${AO_DEVKIT_BASE_IMAGE:-}"
DEFAULT_TEMPLATE_NAME="${AO_DEVKIT_DEFAULT_TEMPLATE:-ao-linux-docker}"
TEMPLATE_DIR="${AO_CLOUD_CODER_TEMPLATE_DIR:-coder}"
DOCKERFILE="${AO_DEVKIT_DOCKERFILE:-coder/devkit.Dockerfile}"
DEVKIT_TAG="${AO_DEVKIT_IMAGE_TAG:-devkit-$(date +%Y%m%d%H%M%S)}"

MED_NAME=ao-devkit;       MED_DISPLAY="AO Dev-kit";   MED_MEM=4096; MED_CPU=1024
MED_DESC="Medium workspace (4 GB RAM). Claude Code, Codex, OpenCode + build-essential, Python 3, ripgrep, jq, tree, vim."
LG_NAME=ao-devkit-large;  LG_DISPLAY="AO Dev-kit-2";  LG_MEM=8192;  LG_CPU=2048
LG_DESC="Large workspace (8 GB RAM). Same tooling as AO Dev-kit on a larger machine."

[[ -f "$TEMPLATE_DIR/main.tf" ]] || { echo "Run from the cloud/ directory ($TEMPLATE_DIR/main.tf not found)." >&2; exit 1; }

secret="$(aws secretsmanager get-secret-value --secret-id "$CODER_SECRET_ID" --query SecretString --output text)"
export CODER_URL CODER_SESSION_TOKEN
CODER_URL="$(printf '%s' "$secret" | python3 -c 'import sys,json;print(json.load(sys.stdin)["url"])')"
CODER_SESSION_TOKEN="$(printf '%s' "$secret" | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')"
unset secret
echo ">> Coder: $CODER_URL"

if [[ -z "$BASE_IMAGE" ]]; then
  echo ">> deriving base image from the live '$DEFAULT_TEMPLATE_NAME' template"
  BASE_IMAGE="$(
    curl -fsS -H "Coder-Session-Token: $CODER_SESSION_TOKEN" "$CODER_URL/api/v2/templates" |
    python3 -c "import sys,json;ts=json.load(sys.stdin);print(next((t['active_version_id'] for t in ts if t['name']=='$DEFAULT_TEMPLATE_NAME'),''))" |
    { read -r vid; curl -fsS -H "Coder-Session-Token: $CODER_SESSION_TOKEN" "$CODER_URL/api/v2/templateversions/$vid/variables"; } |
    python3 -c "import sys,json;print(next((v['value'] for v in json.load(sys.stdin) if v['name']=='workspace_image'),''))"
  )"
  [[ -n "$BASE_IMAGE" ]] || { echo "could not derive base image from '$DEFAULT_TEMPLATE_NAME'; set AO_DEVKIT_BASE_IMAGE" >&2; exit 1; }
fi
echo ">> base image: $BASE_IMAGE"

echo ">> ecr login"
aws ecr get-login-password | docker login --username AWS --password-stdin "$REG" >/dev/null
echo ">> build dev-kit image (linux/amd64) from base"
docker build --platform linux/amd64 --provenance=false \
  --build-arg "AO_WORKSPACE_IMAGE=${BASE_IMAGE}" \
  -f "$DOCKERFILE" -t "$REG/$ECR_REPO:$DEVKIT_TAG" "$TEMPLATE_DIR"
echo ">> push to ecr"
docker push "$REG/$ECR_REPO:$DEVKIT_TAG"
DIGEST="$(aws ecr describe-images --repository-name "$ECR_REPO" --image-ids "imageTag=$DEVKIT_TAG" --query 'imageDetails[0].imageDigest' --output text)"
DEVKIT_IMAGE="$REG/$ECR_REPO@$DIGEST"
echo ">> dev-kit image: $DEVKIT_IMAGE"

push_template() {
  local name="$1" display="$2" mem="$3" cpu="$4" desc="$5"
  echo ">> push template '$name' (mem=${mem} cpu=${cpu})"
  coder templates push "$name" --directory "$TEMPLATE_DIR" \
    --variable "workspace_image=${DEVKIT_IMAGE}" \
    --variable "workspace_memory_mb=${mem}" \
    --variable "workspace_cpu_shares=${cpu}" \
    --yes
  coder templates edit "$name" --display-name "$display" --description "$desc" || true
  echo ">> published '$name' ($display)"
}

push_template "$MED_NAME" "$MED_DISPLAY" "$MED_MEM" "$MED_CPU" "$MED_DESC"
push_template "$LG_NAME"  "$LG_DISPLAY"  "$LG_MEM"  "$LG_CPU"  "$LG_DESC"
echo ">> done"
