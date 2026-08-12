#!/usr/bin/env sh
set -eu
example_root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
cd "$example_root"
: "${ORIGIN_KAFKA_BROKERS:=192.168.8.3:9092}"
export ORIGIN_KAFKA_BROKERS
exec go run ./examples/17-kafka/02-consumer-service-handler start --app-name kafka-consumer --config ./examples/17-kafka/02-consumer-service-handler/config --node kafka-consumer-1
