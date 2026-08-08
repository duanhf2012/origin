#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/10-diagnostics-and-pprof/01-diagnostics-snapshot start --app-name diagnostics-snapshot --config ./examples/10-diagnostics-and-pprof/01-diagnostics-snapshot/config --node game-1
