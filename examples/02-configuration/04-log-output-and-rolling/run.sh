#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/02-configuration/04-log-output-and-rolling start --app-name log-output --config ./examples/02-configuration/04-log-output-and-rolling/config --node log-1
