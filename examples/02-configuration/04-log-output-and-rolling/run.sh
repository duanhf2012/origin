#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec sh "$root/../../12-logging/04-file-rotation/run.sh"
