#!/usr/bin/env bash

set -euo pipefail

# 生产就绪检查只编排仓库已有的可重复门禁。每个真实基础设施脚本都使用
# 独立 Compose Project、端口和临时数据卷，结束后自行清理。
readiness_image="order-system:readiness-$$"
image_created=false
gate_names=()
gate_durations=()

cleanup() {
	status=$?
	trap - EXIT INT TERM
	if [[ "$image_created" == true ]]; then
		docker image rm "$readiness_image" >/dev/null 2>&1 || true
	fi
	exit "$status"
}
trap cleanup EXIT INT TERM

run_gate() {
	name=$1
	shift
	started_at=$(date +%s)
	echo
	echo "==> ${name}"
	if "$@"; then
		finished_at=$(date +%s)
		gate_names+=("$name")
		gate_durations+=("$((finished_at - started_at))s")
		echo "PASS: ${name}"
		return 0
	fi
	echo "FAIL: ${name}" >&2
	return 1
}

verify_release_image() {
	docker build --tag "$readiness_image" .
	image_created=true
	configured_user=$(docker image inspect --format '{{.Config.User}}' "$readiness_image")
	if [[ "$configured_user" != "app" ]]; then
		echo "release image user is ${configured_user:-root}, want app" >&2
		return 1
	fi
	docker run --rm --entrypoint /bin/sh "$readiness_image" -c \
		'test "$(id -u)" -ne 0 && test -x /app/api && test -x /app/migrate'
}

print_summary() {
	echo
	echo "Production readiness automated matrix"
	for ((index = 0; index < ${#gate_names[@]}; index++)); do
		printf '  PASS  %-52s %s\n' "${gate_names[$index]}" "${gate_durations[$index]}"
	done
	echo
	echo "Automated release-candidate evidence passed. External production sign-offs remain required."
}

run_gate "module checksum integrity" go mod verify
run_gate "source, race, contract and configuration gates" make ci
run_gate "immutable non-root release image" verify_release_image
run_gate "real infrastructure lifecycle and outage drills" make integration-check
run_gate "migration, backup and restore drill" make database-recovery-check
run_gate "isolated HTTP performance baseline" make performance-check
run_gate "canary rollout and rollback smoke drill" make rollout-check

print_summary
