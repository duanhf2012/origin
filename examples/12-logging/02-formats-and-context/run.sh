#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/12-logging/02-formats-and-context start --app-name format-context --config ./examples/12-logging/02-formats-and-context/config --node game-1
