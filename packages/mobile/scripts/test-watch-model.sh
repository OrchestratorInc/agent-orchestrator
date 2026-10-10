#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
build=$(mktemp -d)
trap 'rm -rf "$build"' EXIT
swiftc watch-model/Sources/WatchModel/*.swift watch-model/Tests/WatchModelTests/*.swift -o "$build/watch-tests"
"$build/watch-tests"
