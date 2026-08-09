#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/10-admin-diagnostics-and-pprof/04-admin-diagnostics start --app-name admin-diagnostics --config ./examples/10-admin-diagnostics-and-pprof/04-admin-diagnostics/config --node game-1 --admin 127.0.0.1:6063
