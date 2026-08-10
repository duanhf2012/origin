#!/usr/bin/env sh
set -eu
# 前台启动示例，并把控制命令所需的 PID 文件限制在示例目录。
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root"
exec go run ./examples/09-retire-and-resume/01-service-retire-resume start --app-name service-retire --config ./examples/09-retire-and-resume/01-service-retire-resume/config --pid-dir ./examples/09-retire-and-resume/01-service-retire-resume/run --node game-1
