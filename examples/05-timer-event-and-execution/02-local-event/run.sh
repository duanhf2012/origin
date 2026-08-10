#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/05-timer-event-and-execution/02-local-event start --app-name event-demo --config ./examples/05-timer-event-and-execution/02-local-event/config --node game-1
