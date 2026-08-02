#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/03-service-and-module/01-first-service start --app-name first-service --config ./examples/03-service-and-module/01-first-service/config --node game-1
