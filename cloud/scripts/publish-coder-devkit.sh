#!/usr/bin/env bash
set -euo pipefail

# Publish the two AO Dev-kit Coder templates (medium + large) to a Coder
# deployment. Both reuse coder/main.tf and the dev-kit image (coder/devkit.Dockerfile,
# = the approved workspace image + plain developer tooling). They differ only by
# the per-workspace memory/CPU limits set at push time, so the picker shows two
# entries with no size/startup form controls (main.tf exposes those as template
# variables, not coder_parameters).
#
# Prerequisites:
#   - Run where the Coder provisioner's Docker daemon can see the built image
#     (the single-host Azure Coder VM uses local image tags), OR push the image
#     to a registry that the provisioner pulls from and pass its ref.
#   - `coder` CLI installed. Auth is taken from CODER_URL + CODER_SESSION_TOKEN
#     (resolved below from the coder secret), so no interactive `coder login`.
#   - The approved base workspace image must already exist (publish-coder-workspace.sh);
#     pass its ref as AO_DEVKIT_BASE_IMAGE.
#
# Run from the cloud/ directory.

AWS_REGION="${AWS_REGION:-eu-north-1}"
CODER_SECRET_ID="${AO_CLOUD_CODER_SECRET_ID:-ao-cloud/production/coder}"
BASE_IMAGE="${AO_DEVKIT_BASE_IMAGE:-ao-coder-workspace:local}"
DEVKIT_IMAGE="${AO_DEVKIT_IMAGE:-ao-coder-workspace-devkit:local}"
TEMPLATE_DIR="${AO_CLOUD_CODER_TEMPLATE_DIR:-coder}"
DOCKERFILE="${AO_DEVKIT_DOCKERFILE:-coder/devkit.Dockerfile}"

# Two presets. Medium and large are bounded by the shared host (the Azure Coder
# VM is 2 vCPU / 8 GB), so these leave headroom for concurrent workspaces.
MED_NAME="${AO_DEVKIT_MED_NAME:-ao-devkit}"
MED_DISPLAY="${AO_DEVKIT_MED_DISPLAY:-AO Dev-kit}"
MED_MEM_MB="${AO_DEVKIT_MED_MEM_MB:-3072}"
MED_CPU_SHARES="${AO_DEVKIT_MED_CPU_SHARES:-1024}"
MED_DESC="${AO_DEVKIT_MED_DESC:-Medium workspace (3 GB RAM). Claude Code, Codex, OpenCode + build-essential, Python 3, ripgrep, jq, tree, vim.}"

LG_NAME="${AO_DEVKIT_LG_NAME:-ao-devkit-large}"
LG_DISPLAY="${AO_DEVKIT_LG_DISPLAY:-AO Dev-kit-2}"
LG_MEM_MB="${AO_DEVKIT_LG_MEM_MB:-6144}"
LG_CPU_SHARES="${AO_DEVKIT_LG_CPU_SHARES:-2048}"
LG_DESC="${AO_DEVKIT_LG_DESC:-Large workspace (6 GB RAM). Same tooling as AO Dev-kit on a larger machine.}"

if [[ ! -f "$TEMPLATE_DIR/main.tf" ]]; then
    echo "Run from the cloud/ directory ($TEMPLATE_DIR/main.tf not found)." >&2
    exit 1
fi

# Resolve Coder URL + token for the CLI (never printed).
secret="$(aws secretsmanager get-secret-value --secret-id "$CODER_SECRET_ID" --region "$AWS_REGION" ${AWS_PROFILE:+--profile "$AWS_PROFILE"} --query SecretString --output text)"
export CODER_URL CODER_SESSION_TOKEN
CODER_URL="$(printf '%s' "$secret" | python3 -c 'import sys,json;print(json.load(sys.stdin)["url"])')"
CODER_SESSION_TOKEN="$(printf '%s' "$secret" | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')"
unset secret
echo ">> Coder: $CODER_URL"

echo ">> build dev-kit image ($DEVKIT_IMAGE) from base ($BASE_IMAGE)"
docker build --build-arg "AO_WORKSPACE_IMAGE=${BASE_IMAGE}" -f "$DOCKERFILE" -t "$DEVKIT_IMAGE" "$TEMPLATE_DIR"

push_template() {
    local name="$1" display="$2" mem="$3" cpu="$4" desc="$5"
    echo ">> push template '$name' (mem=${mem}MB cpu_shares=${cpu})"
    coder templates push "$name" \
        --directory "$TEMPLATE_DIR" \
        --variable "workspace_image=${DEVKIT_IMAGE}" \
        --variable "workspace_memory_mb=${mem}" \
        --variable "workspace_cpu_shares=${cpu}" \
        --yes
    coder templates edit "$name" --display-name "$display" --description "$desc"
    echo ">> published '$name' (display '$display')"
}

push_template "$MED_NAME" "$MED_DISPLAY" "$MED_MEM_MB" "$MED_CPU_SHARES" "$MED_DESC"
push_template "$LG_NAME"  "$LG_DISPLAY"  "$LG_MEM_MB"  "$LG_CPU_SHARES"  "$LG_DESC"

echo ">> done. Both templates should now appear in the AO 'New cloud project' template picker."
