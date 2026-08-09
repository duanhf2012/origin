#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/10-admin-diagnostics-and-pprof/02-admin-application-control start --app-name admin-application-control --config ./examples/10-admin-diagnostics-and-pprof/02-admin-application-control/config --node game-1 --admin 127.0.0.1:6062
