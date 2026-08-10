#!/usr/bin/env sh
set -eu
export ORIGIN_TUTORIAL_REGION=cn-east
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/02-configuration/05-json-and-split-files start --app-name split-config --config ./examples/02-configuration/05-json-and-split-files/config --node game-1,game-2
