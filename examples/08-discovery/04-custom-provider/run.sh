#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/08-discovery/04-custom-provider start --app-name custom-provider --config ./examples/08-discovery/04-custom-provider/config --node game-1
