#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/01-first-application/01-application-node-service start --app-name first-application --config ./examples/01-first-application/01-application-node-service/config --node gateway-1,game-1
