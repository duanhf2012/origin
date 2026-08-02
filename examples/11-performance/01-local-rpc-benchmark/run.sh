#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go test ./tests/integration/rpcfixture -run '^$' -bench '^BenchmarkGeneratedLocalAwait$' -benchmem -benchtime=3s
