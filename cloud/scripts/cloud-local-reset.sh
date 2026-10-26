#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
state_root="${AO_DATA_DIR:-$HOME/.ao}"
data_directory="${AO_CLOUD_LOCAL_POSTGRES_DATA_DIR:-$state_root/cloud/postgres}"
minio_directory="${AO_CLOUD_LOCAL_MINIO_DATA_DIR:-$state_root/cloud/minio}"
namespace="${COMPOSE_PROJECT_NAME:-ao-cloud-local}"
source "$root/scripts/lib/docker-local.sh"

# Resolve symlinks and ".." first, so a path such as ~/.ao/../x cannot pass
# the prefix check.
resolve() { python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "$1"; }
state_root="$(resolve "$state_root")"
data_directory="$(resolve "$data_directory")"
minio_directory="$(resolve "$minio_directory")"
for directory in "$data_directory" "$minio_directory"; do
	case "$directory" in
	"$state_root"/*) ;;
	*)
		echo "Refusing to remove local Cloud data outside ${state_root}: ${directory}" >&2
		exit 1
		;;
	esac
done

cd "$root"
ao_docker_load_teardown_keys
ao_docker_remove_workers "$namespace"
docker compose down --remove-orphans
ao_docker_remove_workspaces "$namespace"
rm -rf "$data_directory" "$minio_directory"
