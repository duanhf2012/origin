#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/03-logging/01-global-and-service start --app-name logging-scope --config ./examples/03-logging/01-global-and-service/config --node game-1
