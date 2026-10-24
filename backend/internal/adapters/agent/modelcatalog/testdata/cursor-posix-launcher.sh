#!/usr/bin/env bash
set -e
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
NODE_BIN="$SCRIPT_DIR/node"
exec -a "$0" "$NODE_BIN" "$SCRIPT_DIR/index.js" "$@"
