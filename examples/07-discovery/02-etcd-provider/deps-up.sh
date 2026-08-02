#!/usr/bin/env sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$root/deploy/compose"
exec docker compose -f base-compose.yml up -d etcd1 etcd2 etcd3
