#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/04-timer-event-and-execution/03-await-and-safe start --app-name execution-demo --config ./examples/04-timer-event-and-execution/03-await-and-safe/config --node game-1
