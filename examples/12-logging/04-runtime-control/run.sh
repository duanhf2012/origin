#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/12-logging/04-runtime-control start --app-name runtime-control --config ./examples/12-logging/04-runtime-control/config --node game-1
