#!/usr/bin/env sh
set -eu
example_root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$example_root"
: "${ORIGIN_MONGODB_URI:=mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true}"
export ORIGIN_MONGODB_URI
exec go run ./examples/15-mongodb/01-game-store start \
  --app-name mongodb-game-store \
  --config ./examples/15-mongodb/01-game-store/config --node mongodb-1
