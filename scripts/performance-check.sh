#!/usr/bin/env bash

set -euo pipefail

# 性能验收使用独立 Compose Project 和独立端口，避免触碰开发环境。
project_name="order-system-performance-$$"
api_port="${PERFORMANCE_API_PORT:-28081}"
postgres_port="${PERFORMANCE_POSTGRES_PORT:-25433}"
redis_port="${PERFORMANCE_REDIS_PORT:-26380}"
kafka_port="${PERFORMANCE_KAFKA_PORT:-29093}"

export API_HOST_PORT="$api_port"
export POSTGRES_HOST_PORT="$postgres_port"
export REDIS_HOST_PORT="$redis_port"
export KAFKA_HOST_PORT="$kafka_port"

api_url="http://127.0.0.1:${api_port}"

compose() {
	docker compose -p "$project_name" "$@"
}

cleanup() {
	status=$?
	trap - EXIT INT TERM
	if [[ $status -ne 0 ]]; then
		echo "performance check failed; application logs follow" >&2
		compose --profile application logs --no-color --tail 200 api postgres redis redpanda >&2 || true
	fi
	compose --profile application down --volumes --remove-orphans --rmi local >/dev/null 2>&1 || true
	exit "$status"
}
trap cleanup EXIT INT TERM

echo "starting isolated PostgreSQL, Redis, Redpanda and API stack"
compose --profile application up -d --build --wait --wait-timeout 180 api

echo "measuring cached catalog detail reads"
go run ./tests/load \
	-url "${api_url}/api/v1/products/product-1" \
	-duration "${PERFORMANCE_DURATION:-10s}" \
	-concurrency "${PERFORMANCE_CONCURRENCY:-20}" \
	-timeout "${PERFORMANCE_TIMEOUT:-2s}" \
	-min-rps "${PERFORMANCE_MIN_RPS:-50}" \
	-max-p95 "${PERFORMANCE_MAX_P95:-500ms}" \
	-max-error-rate "${PERFORMANCE_MAX_ERROR_RATE:-0.01}"

echo "measuring liveness endpoint"
go run ./tests/load \
	-url "${api_url}/healthz" \
	-duration "${PERFORMANCE_HEALTH_DURATION:-5s}" \
	-concurrency "${PERFORMANCE_HEALTH_CONCURRENCY:-20}" \
	-timeout "${PERFORMANCE_TIMEOUT:-2s}" \
	-min-rps "${PERFORMANCE_HEALTH_MIN_RPS:-100}" \
	-max-p95 "${PERFORMANCE_HEALTH_MAX_P95:-250ms}" \
	-max-error-rate "${PERFORMANCE_MAX_ERROR_RATE:-0.01}"

echo "performance baseline passed"
