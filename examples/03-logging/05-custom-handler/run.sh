#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/03-logging/05-custom-handler start --app-name custom-handler --config ./examples/03-logging/05-custom-handler/config --node handler-1
