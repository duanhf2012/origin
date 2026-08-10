#!/usr/bin/env sh
set -eu
# 请求运行中的示例 Application 优雅停止。
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/09-retire-and-resume/01-service-retire-resume stop --app-name service-retire --pid-dir ./examples/09-retire-and-resume/01-service-retire-resume/run --timeout 30s
