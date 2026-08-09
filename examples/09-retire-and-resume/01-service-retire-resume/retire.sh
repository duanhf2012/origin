#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/09-retire-and-resume/01-service-retire-resume retire --app-name service-retire --pid-dir ./examples/09-retire-and-resume/01-service-retire-resume/run --timeout 30s
