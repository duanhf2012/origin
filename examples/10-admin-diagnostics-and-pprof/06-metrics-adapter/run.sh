#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/10-admin-diagnostics-and-pprof/06-metrics-adapter start --app-name metrics-adapter --config ./examples/10-admin-diagnostics-and-pprof/06-metrics-adapter/config --node game-1
