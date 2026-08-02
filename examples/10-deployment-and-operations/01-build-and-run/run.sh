#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
"$root/examples/10-deployment-and-operations/01-build-and-run/build.sh"
cd "$root"
exec ./examples/10-deployment-and-operations/01-build-and-run/bin/hello-service start --app-name deployed-hello --config ./examples/00-quickstart/01-hello-service/config --node hello-1
