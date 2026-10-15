#!/usr/bin/env bash

set -euo pipefail

# 这个演练只启动两个使用内存存储的本地 API 进程，验证 readiness、灰度请求和回滚边界。
# 它不连接 PostgreSQL、Redis、Kafka，也不会操作现有 Docker 容器。
old_port="${ROLLOUT_OLD_PORT:-28082}"
new_port="${ROLLOUT_NEW_PORT:-28083}"
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/order-system-rollout.XXXXXX")
binary="${work_dir}/order-system-api"
old_log="${work_dir}/old.log"
new_log="${work_dir}/new.log"
old_pid=""
new_pid=""

cleanup() {
	status=$?
	trap - EXIT INT TERM
	for pid in "$new_pid" "$old_pid"; do
		if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
			kill -TERM "$pid" 2>/dev/null || true
			wait "$pid" 2>/dev/null || true
		fi
	done
	if [[ $status -ne 0 ]]; then
		echo "rollout check failed; old release log follows" >&2
		sed -n '1,160p' "$old_log" >&2 || true
		echo "new release log follows" >&2
		sed -n '1,160p' "$new_log" >&2 || true
	fi
	rm -rf "$work_dir"
	exit "$status"
}
trap cleanup EXIT INT TERM

wait_for_ready() {
	url=$1
	for _ in {1..30}; do
		if curl --silent --fail --max-time 1 "$url/readyz" >/dev/null; then
			return 0
		fi
		sleep 0.2
	done
	echo "${url} did not become ready" >&2
	return 1
}

assert_products() {
	url=$1
	curl --silent --fail --max-time 2 "$url/api/v1/products/product-1" | \
		grep -q 'Mechanical Keyboard'
}

echo "building one immutable local release binary"
CGO_ENABLED=0 go build -trimpath -o "$binary" ./cmd/api

echo "starting old release on ${old_port}"
HTTP_ADDR=":${old_port}" \
	APP_NAME=order-system-old \
	STORAGE_DRIVER=memory \
	MESSAGE_DRIVER=direct \
	"$binary" >"$old_log" 2>&1 &
old_pid=$!
old_url="http://127.0.0.1:${old_port}"
wait_for_ready "$old_url"
assert_products "$old_url"

echo "starting new release on ${new_port}"
HTTP_ADDR=":${new_port}" \
	APP_NAME=order-system-new \
	STORAGE_DRIVER=memory \
	MESSAGE_DRIVER=direct \
	"$binary" >"$new_log" 2>&1 &
new_pid=$!
new_url="http://127.0.0.1:${new_port}"
wait_for_ready "$new_url"

echo "sending canary requests to the new release"
for _ in {1..10}; do
	assert_products "$new_url"
done

echo "simulating rollback by stopping the new release"
kill -TERM "$new_pid"
wait "$new_pid" || true
new_pid=""
if curl --silent --fail --max-time 1 "$new_url/readyz" >/dev/null 2>&1; then
	echo "new release still serves traffic after rollback" >&2
	exit 1
fi

echo "verifying old release continues serving traffic"
assert_products "$old_url"
echo "rollout and rollback check passed"
