#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/05-rpc-basics/02-async-and-notify start --app-name rpc-async --config ./examples/05-rpc-basics/02-async-and-notify/config --node game-1
