#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/03-logging/03-file-rotation start --app-name log-output --config ./examples/03-logging/03-file-rotation/config --node log-1
