#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/02-configuration/02-default-and-override start --app-name config-override --config ./examples/02-configuration/02-default-and-override/config --node game-1
