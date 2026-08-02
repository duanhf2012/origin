#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/03-service-and-module/02-module-lifecycle start --app-name module-lifecycle --config ./examples/03-service-and-module/02-module-lifecycle/config --node game-1
