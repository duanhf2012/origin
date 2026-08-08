#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/06-rpc-basics/02-async-and-notify start --app-name rpc-async --config ./examples/06-rpc-basics/02-async-and-notify/config --node game-1
