#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/08-retire-and-resume/02-node-and-application start --app-name app-retire --config ./examples/08-retire-and-resume/02-node-and-application/config --node upstream-1,downstream-1
