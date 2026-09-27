.PHONY: fmt test race vet check db-up db-down db-migrate test-integration run-postgres

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
	docker compose down

db-migrate:
	DATABASE_URL='$(DATABASE_URL)' go run ./cmd/migrate

test-integration:
	TEST_DATABASE_URL='$(DATABASE_URL)' go test -race -count=1 ./internal/adapters/postgres

run-postgres:
	STORAGE_DRIVER=postgres DATABASE_URL='$(DATABASE_URL)' go run ./cmd/api
