#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/09-retire-and-resume/03-include-retired start --app-name include-retired --config ./examples/09-retire-and-resume/03-include-retired/config --node game-1
