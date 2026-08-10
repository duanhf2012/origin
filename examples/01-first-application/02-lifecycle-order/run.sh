#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/01-first-application/02-lifecycle-order start --app-name lifecycle-order --config ./examples/01-first-application/02-lifecycle-order/config --node lifecycle-1
