#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/02-configuration/03-service-module-config start --app-name module-config --config ./examples/02-configuration/03-service-module-config/config --node game-1
