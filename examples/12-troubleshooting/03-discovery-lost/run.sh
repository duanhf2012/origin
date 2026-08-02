#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/07-discovery/03-watch-and-lost start --app-name lost-lab --config ./examples/07-discovery/03-watch-and-lost/config --node watcher-1
