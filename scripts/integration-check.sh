#!/usr/bin/env bash

set -euo pipefail

# 使用独立 Compose Project 和独立宿主机端口，避免复用开发环境的数据卷，
# 也避免验收完成后的清理误伤普通 order-system 容器。
project_name="order-system-integration-$$"
api_port="${INTEGRATION_API_PORT:-28080}"
postgres_port="${INTEGRATION_POSTGRES_PORT:-25432}"
redis_port="${INTEGRATION_REDIS_PORT:-26379}"
kafka_port="${INTEGRATION_KAFKA_PORT:-29092}"

export API_HOST_PORT="$api_port"
export POSTGRES_HOST_PORT="$postgres_port"
export REDIS_HOST_PORT="$redis_port"
export KAFKA_HOST_PORT="$kafka_port"

api_url="http://127.0.0.1:${api_port}"
database_url="postgres://order_system:order_system@127.0.0.1:${postgres_port}/order_system?sslmode=disable"
redis_url="redis://127.0.0.1:${redis_port}/0"
kafka_brokers="127.0.0.1:${kafka_port}"

compose() {
	docker compose -p "$project_name" "$@"
}

cleanup() {
	status=$?
	trap - EXIT INT TERM
	if [[ $status -ne 0 ]]; then
		echo "integration check failed; dependency and application logs follow" >&2
		compose --profile application logs --no-color --tail 200 api postgres redis redpanda >&2 || true
	fi
	compose --profile application down --volumes --remove-orphans --rmi local >/dev/null 2>&1 || true
	exit "$status"
}
trap cleanup EXIT INT TERM

wait_for_http_status() {
	url=$1
	want=$2
	attempts=${3:-30}
	for ((attempt = 1; attempt <= attempts; attempt++)); do
		got=$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 4 "$url" || true)
		if [[ "$got" == "$want" ]]; then
			return 0
		fi
		sleep 1
	done
	echo "${url} did not return ${want}; last status was ${got:-unavailable}" >&2
	return 1
}

create_integration_topics() {
	for topic in \
		orders.created.v1.integration \
		orders.created.v1.integration.poison \
		orders.created.v1.integration.dlq; do
		compose exec -T redpanda rpk -X brokers=redpanda:9092 topic create "$topic" \
			--partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists >/dev/null
	done
}

drill_dependency_outage() {
	dependency=$1
	echo "drilling ${dependency} outage"
	compose stop "$dependency" >/dev/null
	wait_for_http_status "${api_url}/readyz" 503 30
	wait_for_http_status "${api_url}/healthz" 200 10
	compose up -d --wait --wait-timeout 90 "$dependency" >/dev/null
	wait_for_http_status "${api_url}/readyz" 200 60
}

echo "starting isolated PostgreSQL, Redis, Redpanda and API stack"
compose --profile application up -d --build --wait --wait-timeout 180 api
create_integration_topics

echo "running PostgreSQL adapter integration tests"
TEST_DATABASE_URL="$database_url" go test -race -count=1 ./internal/adapters/postgres

echo "running Redis adapter integration tests"
TEST_REDIS_URL="$redis_url" go test -race -count=1 ./internal/adapters/redis

echo "running Kafka adapter integration tests"
TEST_KAFKA_BROKERS="$kafka_brokers" go test -race -count=1 ./internal/adapters/messaging

echo "running public HTTP end-to-end order lifecycle"
TEST_API_URL="$api_url" go test -count=1 ./tests/e2e

# liveness 只证明进程仍能服务 HTTP；readiness 必须在关键依赖不可用时摘流，
# 并在依赖恢复后自动回到 200。
drill_dependency_outage redis
drill_dependency_outage redpanda
drill_dependency_outage postgres

echo "integration checks and dependency outage drills passed"
