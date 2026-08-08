#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/07-remote-rpc/01-tcp-two-nodes start --app-name remote-tcp --config ./examples/07-remote-rpc/01-tcp-two-nodes/config --node discovery-1,player-1,gateway-1
