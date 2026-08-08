#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/05-timer-event-and-execution/01-delay-and-cron start --app-name timer-demo --config ./examples/05-timer-event-and-execution/01-delay-and-cron/config --node game-1
