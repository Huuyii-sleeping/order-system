.PHONY: fmt test race vet check db-up db-down db-migrate cache-up cache-down broker-up broker-down infra-up infra-down app-up app-down observability-check observability-up observability-down test-integration test-redis-integration test-kafka-integration run-postgres run-postgres-redis run-kafka run-kafka-redis run-kafka-traced

DATABASE_URL ?= postgres://order_system:order_system@127.0.0.1:5432/order_system?sslmode=disable
REDIS_URL ?= redis://127.0.0.1:6379/0

fmt:
	gofmt -w .

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

check: test race vet

db-up:
	docker compose up -d --wait postgres
	$(MAKE) db-migrate

db-down:
	docker compose stop postgres

cache-up:
	docker compose up -d --wait redis

cache-down:
	docker compose stop redis

broker-up:
	docker compose up -d --wait redpanda
	docker compose exec -T redpanda rpk topic create orders.created.v1 --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create payments.succeeded.v1 --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create payments.failed.v1 --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.dlq --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.poison --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.dlq --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists

broker-down:
	docker compose stop redpanda

infra-up:
	docker compose up -d --wait postgres redpanda redis
	$(MAKE) db-migrate
	docker compose exec -T redpanda rpk topic create orders.created.v1 --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create payments.succeeded.v1 --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create payments.failed.v1 --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.dlq --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.poison --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.dlq --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists

infra-down:
	docker compose down

app-up:
	docker compose --profile application up -d --build --wait api

app-down:
	docker compose --profile application stop api

observability-check:
	docker compose run --rm --no-deps --entrypoint /bin/promtool prometheus check config /etc/prometheus/prometheus.yml
	docker compose run --rm --no-deps --entrypoint /bin/promtool prometheus test rules /etc/prometheus/tests/order-system.test.yml
	docker compose run --rm --no-deps tempo -config.file=/etc/tempo/tempo.yml -config.verify=true

observability-up: observability-check
	docker compose up -d --wait prometheus tempo grafana

observability-down:
	docker compose stop grafana tempo prometheus

db-migrate:
	DATABASE_URL='$(DATABASE_URL)' go run ./cmd/migrate

test-integration:
	TEST_DATABASE_URL='$(DATABASE_URL)' go test -race -count=1 ./internal/adapters/postgres

test-redis-integration:
	TEST_REDIS_URL='$(REDIS_URL)' go test -race -count=1 ./internal/adapters/redis

test-kafka-integration:
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.poison --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.dlq --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	TEST_KAFKA_BROKERS='127.0.0.1:19092' go test -race -count=1 ./internal/adapters/messaging

run-postgres:
	STORAGE_DRIVER=postgres DATABASE_URL='$(DATABASE_URL)' go run ./cmd/api

run-postgres-redis:
	STORAGE_DRIVER=postgres DATABASE_URL='$(DATABASE_URL)' \
	CACHE_DRIVER=redis REDIS_URL='$(REDIS_URL)' go run ./cmd/api

run-kafka:
	STORAGE_DRIVER=postgres DATABASE_URL='$(DATABASE_URL)' \
	MESSAGE_DRIVER=kafka KAFKA_BROKERS='127.0.0.1:19092' go run ./cmd/api

run-kafka-redis:
	CACHE_DRIVER=redis REDIS_URL='$(REDIS_URL)' $(MAKE) run-kafka

run-kafka-traced:
	OTEL_EXPORTER_OTLP_TRACES_ENDPOINT='http://127.0.0.1:4318/v1/traces' $(MAKE) run-kafka
