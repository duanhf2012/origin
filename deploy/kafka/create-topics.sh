#!/usr/bin/env bash
set -euo pipefail

container="${KAFKA_CONTAINER:-origin-kafka}"
bootstrap="${KAFKA_INTERNAL_BROKER:-origin-kafka:19092}"
prefix="${ORIGIN_KAFKA_TOPIC_PREFIX:-origin-kafka}"

create_topic() {
  local topic="$1"
  shift
  docker exec "$container" /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "$bootstrap" \
    --create --if-not-exists \
    --topic "$topic" \
    --partitions 3 \
    --replication-factor 1 \
    "$@"
}

create_topic "${prefix}-raw"
create_topic "${prefix}-json"
create_topic "${prefix}-pb"
create_topic "${prefix}-consumer"
create_topic "${prefix}-recovery"
create_topic "${prefix}-compacted" --config cleanup.policy=compact

docker exec "$container" /opt/kafka/bin/kafka-topics.sh \
  --bootstrap-server "$bootstrap" \
  --list
