#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/10-diagnostics-and-pprof/02-diagnostics-server start --app-name diagnostics-server --config ./examples/10-diagnostics-and-pprof/02-diagnostics-server/config --node game-1 --diagnostics 127.0.0.1:6061
