#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
mkdir -p examples/10-deployment-and-operations/01-build-and-run/bin
exec go build -o examples/10-deployment-and-operations/01-build-and-run/bin/hello-service ./examples/00-quickstart/01-hello-service
