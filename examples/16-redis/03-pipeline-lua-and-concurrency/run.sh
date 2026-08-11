#!/usr/bin/env sh
set -eu
example_root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$example_root"
: "${ORIGIN_REDIS_ADDRESS:=127.0.0.1:6379}"
export ORIGIN_REDIS_ADDRESS
exec go run ./examples/16-redis/03-pipeline-lua-and-concurrency start --app-name redis-atomic --config ./examples/16-redis/03-pipeline-lua-and-concurrency/config --node redis-atomic-1
