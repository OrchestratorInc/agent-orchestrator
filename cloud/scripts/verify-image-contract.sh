#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 2 ]]; then
	echo "usage: $0 CONTROL_PLANE_IMAGE WORKER_IMAGE" >&2
	exit 2
fi

control_image="$1"
worker_image="$2"
work_dir="$(mktemp -d)"
control_container=""
worker_container=""

cleanup() {
	if [[ -n "$control_container" ]]; then
		docker rm -f "$control_container" >/dev/null 2>&1 || true
	fi
	if [[ -n "$worker_container" ]]; then
		docker rm -f "$worker_container" >/dev/null 2>&1 || true
	fi
	rm -rf "$work_dir"
}
trap cleanup EXIT

CONTROL_INSPECT="$(docker image inspect "$control_image")" \
	WORKER_INSPECT="$(docker image inspect "$worker_image")" \
	python3 - <<'PY'
import json
import os

images = {
    "control-plane": (json.loads(os.environ["CONTROL_INSPECT"])[0], ["/ao-cloud"]),
    "worker": (json.loads(os.environ["WORKER_INSPECT"])[0], ["/ao-worker"]),
}
for name, (image, entrypoint) in images.items():
    platform = f'{image.get("Os", "")}/{image.get("Architecture", "")}'
    if platform != "linux/amd64":
        raise SystemExit(f"{name} image platform is {platform}, expected linux/amd64")
    if image.get("Config", {}).get("Entrypoint") != entrypoint:
        raise SystemExit(f"{name} image has an unexpected entrypoint")
    if image.get("Config", {}).get("User") in ("", "0", "root"):
        raise SystemExit(f"{name} image must run as a non-root user")
PY

control_container="$(docker create "$control_image")"
worker_container="$(docker create "$worker_image")"
docker cp "${control_container}:/ao-worker" "$work_dir/control-plane-ao-worker"
docker cp "${control_container}:/ao" "$work_dir/control-plane-ao"
docker cp "${control_container}:/ao-worker-linux-arm64" "$work_dir/control-plane-ao-worker-linux-arm64"
docker cp "${control_container}:/ao-linux-arm64" "$work_dir/control-plane-ao-linux-arm64"
docker cp "${worker_container}:/ao-worker" "$work_dir/worker-ao-worker"

if [[ ! -x "$work_dir/control-plane-ao-worker" || ! -x "$work_dir/worker-ao-worker" ]]; then
	echo "ao-worker must be executable in both images." >&2
	exit 1
fi
if [[ ! -x "$work_dir/control-plane-ao" ]]; then
	echo "Control-plane image must package the AO hook helper." >&2
	exit 1
fi
if [[ ! -x "$work_dir/control-plane-ao-worker-linux-arm64" || ! -x "$work_dir/control-plane-ao-linux-arm64" ]]; then
	echo "Control-plane image must package the linux/arm64 ao-worker and AO hook helper." >&2
	exit 1
fi
# The uploaded worker binaries must be static Linux ELF executables for the
# architecture their path promises: /ao-worker and /ao are linux/amd64, the
# -linux-arm64 siblings are linux/arm64.
python3 - "$work_dir" <<'PY'
import sys

MACHINES = {0x3E: "amd64", 0xB7: "arm64"}
expected = {
    "control-plane-ao-worker": "amd64",
    "control-plane-ao": "amd64",
    "control-plane-ao-worker-linux-arm64": "arm64",
    "control-plane-ao-linux-arm64": "arm64",
}
for name, arch in expected.items():
    with open(f"{sys.argv[1]}/{name}", "rb") as binary:
        header = binary.read(20)
    if header[:4] != b"\x7fELF" or header[4] != 2 or header[5] != 1:
        raise SystemExit(f"{name} is not a 64-bit little-endian ELF executable")
    machine = MACHINES.get(int.from_bytes(header[18:20], "little"), "unknown")
    if machine != arch:
        raise SystemExit(f"{name} targets {machine}, expected {arch}")
PY
if ! cmp -s "$work_dir/control-plane-ao-worker" "$work_dir/worker-ao-worker"; then
	echo "Control-plane and worker images contain different ao-worker binaries." >&2
	exit 1
fi
if ! docker run --rm --entrypoint /bin/sh "$worker_image" -c \
	'command -v claude >/dev/null &&
	 command -v codex >/dev/null &&
	 command -v cursor-agent >/dev/null &&
	 command -v gh >/dev/null &&
	 command -v ao >/dev/null'; then
	echo "Worker image must contain Claude Code, Codex, Cursor Agent, GitHub CLI, and the AO orchestration CLI." >&2
	exit 1
fi

printf 'Verified control-plane %s and worker %s\n' "$control_image" "$worker_image"
