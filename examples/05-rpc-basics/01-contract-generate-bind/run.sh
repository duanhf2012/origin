#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/05-rpc-basics/01-contract-generate-bind start --app-name rpc-bind --config ./examples/05-rpc-basics/01-contract-generate-bind/config --node game-1
