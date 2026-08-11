#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/13-network/03-kcp-raw-self-call start \
  --app-name kcp-raw-self-call \
  --config ./examples/13-network/03-kcp-raw-self-call/config --node network-1
