#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/08-retire-and-resume/01-service-retire-resume start --app-name service-retire --config ./examples/08-retire-and-resume/01-service-retire-resume/config --node game-1 --retired
