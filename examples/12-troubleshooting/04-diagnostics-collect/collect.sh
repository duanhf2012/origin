#!/usr/bin/env sh
set -eu
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
curl "http://127.0.0.1:6063/admin/v1/diagnostics?detail=full" -o "$directory/diagnostics.json"
cat "$directory/diagnostics.json"
