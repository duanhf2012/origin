#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/00-quickstart/01-hello-service start --app-name invalid-config --config ./examples/12-troubleshooting/01-config-error/config
