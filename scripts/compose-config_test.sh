#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)

die() {
	echo "FAIL: $*" >&2
	exit 1
}

if ! command -v docker >/dev/null 2>&1; then
	die "Docker with the Compose plugin is required"
fi
if ! docker compose version >/dev/null 2>&1; then
	die "Docker Compose is required (install the Docker Compose plugin)"
fi

test_root=$(mktemp -d "${TMPDIR:-/tmp}/compose-config-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
env_file=$test_root/.env
config_file=$test_root/config.yaml
default_services_file=$test_root/default-services.txt
production_env_file=$test_root/production.env
production_config_file=$test_root/production-config.yaml
error_file=$test_root/config.stderr
webhook_value=synthetic-webhook-token-0123456789abcdef
deepseek_value=synthetic-deepseek-value
ticktick_value=synthetic-ticktick-value
cat >"$env_file" <<EOF
INDEX01_IMAGE=index-01-hook:local
INDEX01_HOST_PORT=18080
INDEX01_WEBHOOK_TOKEN=$webhook_value
INDEX01_DEEPSEEK_TOKEN=$deepseek_value
INDEX01_TICKTICK_TOKEN=$ticktick_value
INDEX01_TICKTICK_DEFAULT_PROJECT_ID=synthetic-task-project
INDEX01_TICKTICK_NOTE_PROJECT_ID=synthetic-note-project
INDEX01_DEEPSEEK_MODEL=deepseek-v4-flash
INDEX01_TIME_ZONE=UTC
INDEX01_TICKTICK_PROJECT_ALIASES={}
INDEX01_MAX_BODY_BYTES=67108864
INDEX01_WORKER_OWNER=synthetic-worker
EOF
# Keep Compose output in a private temporary file. Never print it or its errors.
if ! docker compose --profile maintenance --env-file "$env_file" -f "$project_dir/compose.yaml" config >"$config_file" 2>"$error_file"; then
	die "docker compose config rejected synthetic values"
fi
if ! docker compose --env-file "$env_file" -f "$project_dir/compose.yaml" config --services >"$default_services_file" 2>"$error_file"; then
	die "docker compose config rejected the default profile"
fi
[ "$(cat "$default_services_file")" = index-01-hook ] ||
	die "the default profile enables more than the application service"

contains() {
	grep -F -- "$1" "$config_file" >/dev/null 2>&1
}
not_contains() {
	if contains "$1"; then
		die "rendered Compose config contains an unexpected value"
	fi
}

contains 'host_ip: 127.0.0.1' || die "Compose port is not loopback-bound"
contains 'published: "18080"' || die "Compose host port is incorrect"
contains 'container_name: index-01-hook' || die "Compose container name is incorrect"
[ "$(grep -c '^  index-01-hook:$' "$config_file")" -eq 1 ] || die "Compose application service is not fixed"
[ "$(grep -c '^  index-01-hook-maintenance:$' "$config_file")" -eq 1 ] || die "Compose maintenance service is missing"
[ "$(grep -c '^    container_name:' "$config_file")" -eq 1 ] || die "Compose defines more than one container"
contains 'source: index-01-hook-data' || die "Compose volume source is missing"
[ "$(grep -c 'source: index-01-hook-data' "$config_file")" -eq 2 ] || die "Compose services do not share the named volume"
contains 'target: /var/lib/index-01-hook' || die "Compose volume target is missing"
contains 'name: index-01-hook-data' || die "Compose volume name is incorrect"
contains 'read_only: true' || die "Compose read-only setting is missing"
contains 'user: 65532:65532' || die "Compose user setting is incorrect"
contains 'cap_drop:' || die "Compose capability drop is missing"
contains 'no-new-privileges:true' || die "Compose no-new-privileges setting is missing"
contains 'test:' || die "Compose healthcheck is missing"
contains '/index-01-hook' || die "Compose healthcheck command is missing"
contains 'healthcheck' || die "Compose healthcheck subcommand is missing"
contains 'stop_grace_period: 20s' || die "Compose graceful stop period is missing"
contains 'INDEX01_DB_PATH: /var/lib/index-01-hook/data/index01.db' || die "Compose database path is incorrect"
contains 'INDEX01_LISTEN_ADDR: :8080' || die "Compose listen address is incorrect"
contains 'profiles:' || die "Compose maintenance profile is missing"
contains '- maintenance' || die "Compose maintenance profile is incorrect"
contains 'network_mode: none' || die "Compose maintenance network isolation is missing"
[ "$(grep -c 'image: index-01-hook:local' "$config_file")" -eq 2 ] || die "Compose services do not share the image"
production_digest=ghcr.io/example/index-01-hook@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
sed "s#INDEX01_IMAGE=index-01-hook:local#INDEX01_IMAGE=$production_digest#" "$env_file" >"$production_env_file"
if ! docker compose --profile maintenance --env-file "$production_env_file" -f "$project_dir/compose.yaml" config >"$production_config_file" 2>"$error_file"; then
	die "docker compose config rejected the production digest"
fi
[ "$(grep -c "image: $production_digest" "$production_config_file")" -eq 2 ] ||
	die "Compose did not propagate the production digest to both services"

maintenance_config=$(awk '
	/^  index-01-hook-maintenance:$/ { in_maintenance=1 }
	in_maintenance && /^volumes:$/ { exit }
	in_maintenance { print }
' "$config_file")
maintenance_environment=$(printf '%s\n' "$maintenance_config" | awk '
	/^    environment:$/ { in_environment=1; next }
	in_environment && /^    [^ ]/ { exit }
	in_environment { print }
')
[ "$(printf '%s\n' "$maintenance_environment" | grep -c '^      [A-Za-z_].*:')" -eq 1 ] || \
	die "maintenance environment contains more than INDEX01_DB_PATH"
printf '%s\n' "$maintenance_environment" | grep -F 'INDEX01_DB_PATH: /var/lib/index-01-hook/data/index01.db' >/dev/null || \
	die "maintenance database path is missing"
for secret_name in INDEX01_WEBHOOK_TOKEN INDEX01_DEEPSEEK_TOKEN INDEX01_TICKTICK_TOKEN INDEX01_LISTEN_ADDR; do
	if printf '%s\n' "$maintenance_config" | grep -F "$secret_name" >/dev/null 2>&1; then
		die "maintenance service contains $secret_name"
	fi
done
if printf '%s\n' "$maintenance_config" | grep -F 'container_name:' >/dev/null 2>&1; then
	die "maintenance service has a fixed container name"
fi
for maintenance_setting in 'network_mode: none' 'read_only: true' 'user: 65532:65532' 'no-new-privileges:true'; do
	printf '%s\n' "$maintenance_config" | grep -F "$maintenance_setting" >/dev/null || \
		die "maintenance service is missing $maintenance_setting"
done

# Compose-only values select rendering and host binding. They must not enter the application environment.
not_contains 'INDEX01_IMAGE:'
not_contains 'INDEX01_HOST_PORT:'
# Check that synthetic values were used without placing private data in diagnostics.
contains "$webhook_value" || die "synthetic webhook value was not rendered"
contains "$deepseek_value" || die "synthetic DeepSeek value was not rendered"
contains "$ticktick_value" || die "synthetic TickTick value was not rendered"

echo "PASS: Compose configuration"
