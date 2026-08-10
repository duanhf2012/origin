#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/07-remote-rpc/03-route-and-broadcast start --app-name route-broadcast --config ./examples/07-remote-rpc/03-route-and-broadcast/config --node discovery-1,player-1,player-2,gateway-1
