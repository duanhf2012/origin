#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/00-quickstart/01-hello-service start --app-name hello-service --config ./examples/00-quickstart/01-hello-service/config --node hello-1
