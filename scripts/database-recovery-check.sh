#!/usr/bin/env bash

set -euo pipefail

# 备份恢复验收只操作独立 Compose Project 和临时目录，不接触开发数据库。
project_name="order-system-recovery-$$"
postgres_port="${RECOVERY_POSTGRES_PORT:-35432}"
export POSTGRES_HOST_PORT="$postgres_port"

source_url="postgres://order_system:order_system@127.0.0.1:${postgres_port}/order_system?sslmode=disable"
restore_url="postgres://order_system:order_system@127.0.0.1:${postgres_port}/order_system_restore?sslmode=disable"
backup_dir=$(mktemp -d "${TMPDIR:-/tmp}/order-system-recovery.XXXXXX")
backup_file="${backup_dir}/order-system.dump"

compose() {
	docker compose -p "$project_name" "$@"
}

cleanup() {
	status=$?
	trap - EXIT INT TERM
	if [[ $status -ne 0 ]]; then
		echo "database recovery check failed; PostgreSQL logs follow" >&2
		compose logs --no-color --tail 200 postgres >&2 || true
	fi
	compose down --volumes --remove-orphans >/dev/null 2>&1 || true
	rm -f "$backup_file"
	rmdir "$backup_dir" 2>/dev/null || true
	exit "$status"
}
trap cleanup EXIT INT TERM

database_fingerprint() {
	database=$1
	compose exec -T postgres psql -X -A -t -v ON_ERROR_STOP=1 \
		-U order_system -d "$database" -c "
			SELECT concat_ws('|',
				(SELECT count(*) FROM schema_migrations),
				(SELECT count(*) FROM schema_migrations WHERE length(checksum) = 64),
				(SELECT email FROM users WHERE id = 'recovery-user'),
				(SELECT status FROM orders WHERE id = 'recovery-order'),
				(SELECT payload ->> 'order_id' FROM outbox_messages WHERE id = 'recovery-outbox')
			)
		"
}

echo "starting isolated PostgreSQL for backup and restore verification"
compose up -d --wait --wait-timeout 90 postgres

echo "applying forward-only migrations"
DATABASE_URL="$source_url" go run ./cmd/migrate

echo "creating synthetic recovery data"
compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U order_system -d order_system <<'SQL'
BEGIN;
INSERT INTO users (id, email, password_hash, created_at)
VALUES ('recovery-user', 'recovery@example.test', 'synthetic-password-hash', NOW());
INSERT INTO products (id, name, price_cents)
VALUES ('recovery-product', 'Recovery Test Product', 12345);
INSERT INTO stocks (product_id, available)
VALUES ('recovery-product', 7);
INSERT INTO orders (
    id, customer_id, product_id, quantity, unit_price_cents,
    total_cents, status, idempotency_key, created_at
) VALUES (
    'recovery-order', 'recovery-user', 'recovery-product', 1, 12345,
    12345, 'pending', 'recovery-idempotency-key', NOW()
);
INSERT INTO outbox_messages (
    id, topic, message_key, payload, occurred_at, next_attempt_at
) VALUES (
    'recovery-outbox', 'orders.created.v1', 'recovery-order',
    '{"order_id":"recovery-order"}'::jsonb, NOW(), NOW()
);
COMMIT;
SQL

source_fingerprint=$(database_fingerprint order_system)
expected_migrations=$(find migrations -maxdepth 1 -type f -name '*.sql' | wc -l | tr -d ' ')
expected_fingerprint="${expected_migrations}|${expected_migrations}|recovery@example.test|pending|recovery-order"
if [[ "$source_fingerprint" != "$expected_fingerprint" ]]; then
	echo "unexpected source fingerprint: ${source_fingerprint}" >&2
	exit 1
fi

echo "creating custom-format PostgreSQL backup"
compose exec -T postgres pg_dump -U order_system -d order_system \
	--format=custom --no-owner --no-acl >"$backup_file"
if [[ ! -s "$backup_file" ]]; then
	echo "pg_dump produced an empty backup" >&2
	exit 1
fi

echo "restoring backup into a fresh database"
compose exec -T postgres createdb -U order_system --template=template0 order_system_restore
compose exec -T postgres pg_restore -U order_system -d order_system_restore \
	--exit-on-error --no-owner --no-acl <"$backup_file"

# 恢复后再次运行当前迁移器，同时验证备份中的版本、文件名和 checksum 没有漂移。
DATABASE_URL="$restore_url" go run ./cmd/migrate
restore_fingerprint=$(database_fingerprint order_system_restore)
if [[ "$restore_fingerprint" != "$source_fingerprint" ]]; then
	echo "restored fingerprint ${restore_fingerprint} differs from source ${source_fingerprint}" >&2
	exit 1
fi

echo "database backup and restore verification passed: ${restore_fingerprint}"
