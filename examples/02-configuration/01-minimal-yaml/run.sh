#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/02-configuration/01-minimal-yaml start --app-name minimal-yaml --config ./examples/02-configuration/01-minimal-yaml/config --node game-1
