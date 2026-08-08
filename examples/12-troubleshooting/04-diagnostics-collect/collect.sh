#!/usr/bin/env sh
set -eu
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
curl http://127.0.0.1:6061/debug/origin/diagnostics -o "$directory/diagnostics.json"
cat "$directory/diagnostics.json"
