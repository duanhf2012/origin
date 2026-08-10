#!/usr/bin/env sh
set -eu
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
output="$directory/diagnostics.json"
temporary="$output.tmp"
trap 'rm -f "$temporary"' EXIT
curl --fail --silent --show-error \
  "http://127.0.0.1:6063/admin/v1/diagnostics?detail=full" \
  -o "$temporary"
mv -f "$temporary" "$output"
trap - EXIT
cat "$output"
