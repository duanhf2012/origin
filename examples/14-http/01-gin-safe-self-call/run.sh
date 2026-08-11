#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/14-http/01-gin-safe-self-call start \
  --app-name gin-safe-self-call \
  --config ./examples/14-http/01-gin-safe-self-call/config --node http-1
