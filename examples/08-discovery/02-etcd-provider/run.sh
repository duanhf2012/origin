#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/08-discovery/02-etcd-provider start --app-name etcd-discovery --config ./examples/08-discovery/02-etcd-provider/config --node battle-room-1,card-room-1
