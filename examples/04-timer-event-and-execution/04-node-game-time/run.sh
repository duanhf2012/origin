#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/04-timer-event-and-execution/04-node-game-time start --app-name node-game-time-demo --config ./examples/04-timer-event-and-execution/04-node-game-time/config --node game-1
