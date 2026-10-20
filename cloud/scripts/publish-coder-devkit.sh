#!/usr/bin/env bash
set -uo pipefail

# Publish the two AO Dev-kit Coder templates (medium + large) to the Coder
# deployment. Both reuse coder/main.tf and the SAME approved workspace image the
# default (ao-linux-docker) template runs -- which is already present on the Coder
# host -- so no new image has to be distributed (the host has no standing ECR pull
# credentials for arbitrary new images). The dev-kit tooling is installed at
# workspace start via the devkit_apt_packages template variable; the default
# template leaves that empty and stays plain. The two templates differ only by the
# per-workspace memory/CPU set at push time, so the picker shows two entries with
# no size/startup form controls.
#
# Runs from a workstation with AWS creds + the coder CLI. Run from cloud/.

export AWS_PROFILE="${AWS_PROFILE:-ao-cloud}"
export AWS_REGION="${AWS_REGION:-eu-north-1}"
CODER_SECRET_ID="${AO_CLOUD_CODER_SECRET_ID:-ao-cloud/production/coder}"
TEMPLATE_DIR="${AO_CLOUD_CODER_TEMPLATE_DIR:-coder}"
# On the single-host Azure Coder deployment the workspace image is present as a
# local tag (ao-coder-workspace:local) that the docker provider uses without a
# registry pull. That is the reliable default here because the host has no
# standing ECR/registry pull credentials; override with AO_DEVKIT_BASE_IMAGE to
# point at a registry image once the host can authenticate to it.
BASE_IMAGE="${AO_DEVKIT_BASE_IMAGE:-ao-coder-workspace:local}"
DEVKIT_PKGS="${AO_DEVKIT_APT_PACKAGES:-build-essential jq less python3 python3-pip python3-venv ripgrep tree unzip vim}"

MED_NAME=ao-devkit;      MED_DISPLAY="AO Dev-kit (Medium)";   MED_MEM=4096; MED_CPU=1024
MED_DESC="4 GB RAM · Claude + dev tooling"
LG_NAME=ao-devkit-large; LG_DISPLAY="AO Dev-kit (Large)";  LG_MEM=8192;  LG_CPU=2048
LG_DESC="8 GB RAM · Claude + dev tooling"

[[ -f "$TEMPLATE_DIR/main.tf" ]] || { echo "Run from the cloud/ directory ($TEMPLATE_DIR/main.tf not found)." >&2; exit 1; }

secret="$(aws secretsmanager get-secret-value --secret-id "$CODER_SECRET_ID" --query SecretString --output text)"
export CODER_URL CODER_SESSION_TOKEN
CODER_URL="$(printf '%s' "$secret" | python3 -c 'import sys,json;print(json.load(sys.stdin)["url"])')"
CODER_SESSION_TOKEN="$(printf '%s' "$secret" | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')"
unset secret
echo ">> Coder: $CODER_URL"

echo ">> workspace image: $BASE_IMAGE"
echo ">> dev-kit packages: $DEVKIT_PKGS"

push_template() {
  local name="$1" display="$2" mem="$3" cpu="$4" desc="$5"
  echo ">> push template '$name' (mem=${mem} cpu=${cpu})"
  coder templates push "$name" --directory "$TEMPLATE_DIR" \
    --variable "workspace_image=${BASE_IMAGE}" \
    --variable "workspace_memory_mb=${mem}" \
    --variable "workspace_cpu_shares=${cpu}" \
    --variable "devkit_apt_packages=${DEVKIT_PKGS}" \
    --yes || { echo "!! push failed for $name"; return 1; }
  coder templates edit "$name" --display-name "$display" --description "$desc" || true
  echo ">> published '$name' ($display)"
}

push_template "$MED_NAME" "$MED_DISPLAY" "$MED_MEM" "$MED_CPU" "$MED_DESC"
push_template "$LG_NAME"  "$LG_DISPLAY"  "$LG_MEM"  "$LG_CPU"  "$LG_DESC"
echo ">> done"
