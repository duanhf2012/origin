#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/10-admin-diagnostics-and-pprof/01-admin-service-endpoints start --app-name admin-service-endpoints --config ./examples/10-admin-diagnostics-and-pprof/01-admin-service-endpoints/config --node game-1 --admin 127.0.0.1:6061
