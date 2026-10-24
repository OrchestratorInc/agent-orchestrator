#!/bin/sh
basedir=$(dirname "$(echo "$0" | sed -e 's,\\,/,g')")
export NODE_PATH="$basedir/node_modules"
if [ -x "$basedir/node" ]; then
  exec "$basedir/node" "$basedir/node_modules/grok-cli/dist/cli.js" "$@"
else
  exec node "$basedir/node_modules/grok-cli/dist/cli.js" "$@"
fi
