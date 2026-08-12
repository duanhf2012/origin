#!/usr/bin/env sh
set -eu
example_root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$example_root"
: "${ORIGIN_KAFKA_BROKERS:=192.168.8.3:9092}"
export ORIGIN_KAFKA_BROKERS
exec go run ./examples/17-kafka/03-managed-and-native start --app-name kafka-tools --config ./examples/17-kafka/03-managed-and-native/config --node kafka-tools-1
