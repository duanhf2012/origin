#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/08-discovery/01-origin-provider start --app-name origin-discovery --config ./examples/08-discovery/01-origin-provider/config --node discovery-1,game-1
