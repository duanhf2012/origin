#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/07-discovery/05-await-service start --app-name discovery-await --config ./examples/07-discovery/05-await-service/config --node gateway-1
