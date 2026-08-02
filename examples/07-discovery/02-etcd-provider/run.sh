#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/07-discovery/02-etcd-provider start --app-name etcd-discovery --config ./examples/07-discovery/02-etcd-provider/config --node game-1,game-2
