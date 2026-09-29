.PHONY: fmt test race vet check db-up db-down db-migrate broker-up broker-down infra-up infra-down test-integration test-kafka-integration run-postgres run-kafka

DATABASE_URL ?= postgres://order_system:order_system@127.0.0.1:5432/order_system?sslmode=disable

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

broker-up:
	docker compose up -d --wait redpanda
	docker compose exec -T redpanda rpk topic create orders.created.v1 --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.dlq --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.poison --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.dlq --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists

broker-down:
	docker compose stop redpanda

infra-up:
	docker compose up -d --wait postgres redpanda
	$(MAKE) db-migrate
	docker compose exec -T redpanda rpk topic create orders.created.v1 --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.dlq --partitions 3 --replicas 1 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.poison --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.dlq --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists

infra-down:
	docker compose down

db-migrate:
	DATABASE_URL='$(DATABASE_URL)' go run ./cmd/migrate

test-integration:
	TEST_DATABASE_URL='$(DATABASE_URL)' go test -race -count=1 ./internal/adapters/postgres

test-kafka-integration:
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.poison --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	docker compose exec -T redpanda rpk topic create orders.created.v1.integration.dlq --partitions 1 --replicas 1 --topic-config retention.ms=3600000 --if-not-exists
	TEST_KAFKA_BROKERS='127.0.0.1:19092' go test -race -count=1 ./internal/adapters/messaging

run-postgres:
	STORAGE_DRIVER=postgres DATABASE_URL='$(DATABASE_URL)' go run ./cmd/api

run-kafka:
	STORAGE_DRIVER=postgres DATABASE_URL='$(DATABASE_URL)' \
	MESSAGE_DRIVER=kafka KAFKA_BROKERS='127.0.0.1:19092' go run ./cmd/api
