#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/09-diagnostics-and-pprof/03-pprof-toggle start --app-name pprof-toggle --config ./examples/09-diagnostics-and-pprof/03-pprof-toggle/config --node game-1
