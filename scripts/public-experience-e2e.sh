#!/bin/sh
set -eu

# This test is opt-in. It contacts only the pinned local Docker images and the
# synthetic provider fixture.
repo_root=$(cd -- "$(dirname -- "$0")/.." && pwd)
compose_file=$repo_root/e2e/compose.yaml
for required_command in curl docker od openssl python3 xargs; do
    command -v "$required_command" >/dev/null 2>&1 || {
        printf 'public experience E2E failed: %s is required\n' "$required_command" >&2
        exit 1
    }
done
docker info >/dev/null 2>&1 || {
    printf '%s\n' 'public experience E2E failed: Docker daemon is unavailable' >&2
    exit 1
}
docker_context=$(docker context show 2>/dev/null) || exit 1
docker_endpoint=$(docker context inspect "$docker_context" \
    --format '{{(index .Endpoints "docker").Host}}' 2>/dev/null) || exit 1
case "$docker_endpoint" in
    unix:///var/run/docker.sock|unix://"$HOME"/*|unix:///Users/*|unix:///home/*) ;;
    *) printf '%s\n' 'public experience E2E failed: Docker endpoint is not local' >&2; exit 1 ;;
esac
suffix=$(od -An -N6 -tx1 /dev/urandom | tr -d '[:space:]')
project="index01-e2e-$suffix"
fixture_image="index-01-hook-provider-fixture:$suffix"
app_image="index-01-hook-e2e:$suffix"
temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/index01-e2e.XXXXXX")
cert_dir=$temporary_root/certs
event_dir=$temporary_root/events
diagnostics_dir=$temporary_root/diagnostics
mkdir -p "$cert_dir" "$event_dir" "$diagnostics_dir"
chmod 0711 "$temporary_root"
chmod 0755 "$cert_dir"
chmod 0777 "$event_dir"
chmod 0700 "$diagnostics_dir"

db_volume="${project}-data"
resources_started=0
fixture_image_created=0
app_image_created=0
compose() {
    docker compose -p "$project" -f "$compose_file" "$@"
}

cleanup() {
    status=$?
    cleanup_failed=0
    trap - EXIT HUP INT TERM
    if [ "${resources_started:-0}" = 1 ]; then
        compose down --volumes --remove-orphans >/dev/null 2>&1 || cleanup_failed=1
        owned_containers=$(docker ps -aq \
            --filter "label=com.docker.compose.project=$project" 2>/dev/null) || {
            owned_containers=
            cleanup_failed=1
        }
        if [ -n "$owned_containers" ]; then
            printf '%s\n' "$owned_containers" | xargs docker rm -f >/dev/null 2>&1 || cleanup_failed=1
        fi
        if docker volume inspect "$db_volume" >/dev/null 2>&1; then
            docker volume rm -f "$db_volume" >/dev/null 2>&1 || cleanup_failed=1
        fi
        docker volume inspect "$db_volume" >/dev/null 2>&1 && cleanup_failed=1
        for network in "${project}_providers" "${project}_proxy" "${project}_published"; do
            if docker network inspect "$network" >/dev/null 2>&1; then
                docker network rm "$network" >/dev/null 2>&1 || cleanup_failed=1
            fi
            docker network inspect "$network" >/dev/null 2>&1 && cleanup_failed=1
        done
        if docker ps -aq --filter "label=com.docker.compose.project=$project" 2>/dev/null |
            grep . >/dev/null 2>&1; then
            cleanup_failed=1
        fi
    fi
    if [ "${fixture_image_created:-0}" = 1 ]; then
        docker image rm -f "$fixture_image" >/dev/null 2>&1 || cleanup_failed=1
        docker image inspect "$fixture_image" >/dev/null 2>&1 && cleanup_failed=1
    fi
    if [ "${app_image_created:-0}" = 1 ]; then
        docker image rm -f "$app_image" >/dev/null 2>&1 || cleanup_failed=1
        docker image inspect "$app_image" >/dev/null 2>&1 && cleanup_failed=1
    fi
    if [ "${E2E_KEEP_TEMP:-0}" = 1 ]; then
        printf 'E2E diagnostics retained at %s\n' "$temporary_root" >&2
    else
        rm -rf "$temporary_root" || cleanup_failed=1
        [ ! -e "$temporary_root" ] || cleanup_failed=1
    fi
    if [ "$cleanup_failed" -ne 0 ]; then
        printf '%s\n' 'public experience E2E cleanup failed' >&2
        status=1
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

die() {
    printf 'public experience E2E failed: %s\n' "$1" >&2
    exit 1
}

run_or_die() {
    log_file=$1
    shift
    if ! "$@" >"$log_file" 2>&1; then
        die "command failed"
    fi
}

"$repo_root/e2e/generate-certs.sh" "$cert_dir" >"$diagnostics_dir/certs.log" 2>&1 || die "certificate generation failed"

select_loopback_port() {
    python3 - <<'PY'
import socket
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}
caddy_port=$(select_loopback_port) || die "could not select a loopback port"
case "$caddy_port" in
    ''|*[!0-9]*) die "selected Caddy port is invalid" ;;
esac

export E2E_CERT_DIR="$cert_dir"
export E2E_EVENT_DIR="$event_dir"
export E2E_DB_VOLUME="$db_volume"
export E2E_CADDY_PORT="$caddy_port"
export E2E_FIXTURE_IMAGE="$fixture_image"
export INDEX01_IMAGE="$app_image"
export INDEX01_WEBHOOK_TOKEN=synthetic-webhook-e2e-token-0123456789abcdef
export INDEX01_DEEPSEEK_TOKEN=deepseek-e2e-token
export INDEX01_TICKTICK_TOKEN=ticktick-e2e-token

if docker ps -a --filter "label=com.docker.compose.project=$project" --format '{{.ID}}' |
    grep . >"$diagnostics_dir/project-collision.log" 2>&1; then
    die "generated Compose project already exists"
fi
if docker volume inspect "$db_volume" >"$diagnostics_dir/volume-collision.log" 2>&1; then
    die "generated database volume already exists"
fi
if docker image inspect "$fixture_image" >"$diagnostics_dir/image-collision.log" 2>&1; then
    die "generated fixture image already exists"
fi
if docker image inspect "$app_image" >"$diagnostics_dir/app-image-collision.log" 2>&1; then
    die "generated application image already exists"
fi
for network in "${project}_providers" "${project}_proxy" "${project}_published"; do
    if docker network inspect "$network" >"$diagnostics_dir/network-collision.log" 2>&1; then
        die "generated Compose network already exists"
    fi
done
run_or_die "$diagnostics_dir/config.log" compose config --quiet
if ! compose build --pull --no-cache provider-fixture \
    >"$diagnostics_dir/fixture-build.log" 2>&1; then
    if docker image inspect "$fixture_image" >/dev/null 2>&1; then
        fixture_image_created=1
    fi
    die "fixture image build failed"
fi
fixture_image_created=1
if ! compose build --pull --no-cache index-01-hook \
    >"$diagnostics_dir/app-build.log" 2>&1; then
    if docker image inspect "$app_image" >/dev/null 2>&1; then
        app_image_created=1
    fi
    die "application image build failed"
fi
app_image_created=1
run_or_die "$diagnostics_dir/pull.log" compose pull caddy

resources_started=1
run_or_die "$diagnostics_dir/fixture-up.log" compose up -d provider-fixture
fixture_ready=0
for _ in $(seq 1 60); do
    if [ -f "$event_dir/ready" ]; then
        fixture_ready=1
        break
    fi
    sleep 1
done
[ "$fixture_ready" -eq 1 ] || die "fixture did not become ready"

# The application must reject the fixture when it receives an unrelated CA.
if docker run --rm --network "${project}_providers" --read-only --user 65532:65532 \
    --cap-drop ALL --security-opt no-new-privileges:true \
    --mount "type=bind,src=$cert_dir/wrong-ca.crt,dst=/etc/ssl/certs/ca-certificates.crt,readonly" \
    --env INDEX01_TICKTICK_TOKEN=ticktick-e2e-token \
    "$app_image" ticktick-projects >/dev/null 2>&1; then
    die "application accepted an untrusted provider certificate"
fi

run_or_die "$diagnostics_dir/app-up.log" compose up -d index-01-hook
app_ready=0
for _ in $(seq 1 90); do
    if compose exec -T index-01-hook /index-01-hook healthcheck >"$diagnostics_dir/app-health.log" 2>&1; then
        app_ready=1
        break
    fi
    sleep 1
done
[ "$app_ready" -eq 1 ] || die "application did not become ready"

caddy_started=0
for attempt in 1 2 3 4 5; do
    if [ "$attempt" -gt 1 ]; then
        caddy_port=$(select_loopback_port) || die "could not select a loopback port"
        export E2E_CADDY_PORT="$caddy_port"
    fi
    if compose up -d caddy >>"$diagnostics_dir/caddy-up.log" 2>&1; then
        sleep 1
        candidate_id=$(compose ps -q caddy)
        if [ -n "$candidate_id" ] &&
            [ "$(docker inspect --format '{{.State.Running}}' "$candidate_id" 2>/dev/null)" = true ]; then
            caddy_started=1
            break
        fi
    fi
    compose rm -sf caddy >>"$diagnostics_dir/caddy-up.log" 2>&1 || true
done
[ "$caddy_started" -eq 1 ] || die "Caddy failed to bind a loopback port"

providers_network=${project}_providers
proxy_network=${project}_proxy
published_network=${project}_published
[ "$(docker network inspect --format '{{.Internal}}' "$providers_network")" = true ] ||
    die "provider network is not internal"
[ "$(docker network inspect --format '{{.Internal}}' "$proxy_network")" = true ] ||
    die "proxy network is not internal"
[ "$(docker network inspect --format '{{.Internal}}' "$published_network")" = false ] ||
    die "published network is internal"
fixture_id=$(compose ps -q provider-fixture)
app_id=$(compose ps -q index-01-hook)
caddy_id=$(compose ps -q caddy)
[ -z "$(docker port "$fixture_id")" ] || die "fixture publishes a host port"
[ -z "$(docker port "$app_id")" ] || die "application publishes a host port"
[ "$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}} {{end}}' "$fixture_id")" = "$providers_network " ] ||
    die "fixture network topology differs"
app_networks=$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}} {{end}}' "$app_id")
case "$app_networks" in
    "$providers_network $proxy_network "|"$proxy_network $providers_network ") ;;
    *) die "application network topology differs" ;;
esac
caddy_networks=$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}} {{end}}' "$caddy_id")
case "$caddy_networks" in
    "$proxy_network $published_network "|"$published_network $proxy_network ") ;;
    *) die "Caddy network topology differs" ;;
esac
[ "$(docker inspect --format '{{(index (index .NetworkSettings.Ports "443/tcp") 0).HostIp}}' "$caddy_id")" = 127.0.0.1 ] ||
    die "Caddy port is not loopback-bound"
[ "$(docker inspect --format '{{(index (index .NetworkSettings.Ports "443/tcp") 0).HostPort}}' "$caddy_id")" = "$caddy_port" ] ||
    die "Caddy loopback port differs"

base_url="https://public.e2e.test:$caddy_port"
curl_request() {
    response_file=$1
    shift
    curl --cacert "$cert_dir/ca.crt" \
        --noproxy '*' \
        --resolve "public.e2e.test:$caddy_port:127.0.0.1" \
        --silent --show-error --max-time 10 --max-redirs 0 \
        "$@" --output "$response_file" --write-out '%{http_code}' \
        2>"$diagnostics_dir/curl.log"
}

ready=0
for _ in $(seq 1 60); do
    if status_code=$(curl_request "$diagnostics_dir/ready.json" \
        --header "Authorization: Bearer $INDEX01_WEBHOOK_TOKEN" \
        --request GET "$base_url/readyz"); then
        case "$status_code" in
            200|503) ready=1; break ;;
        esac
    fi
    sleep 1
done
[ "$ready" -eq 1 ] || die "Caddy did not become ready"

assert_webhook_auth_failure() {
    name=$1
    shift
    body=$diagnostics_dir/webhook-auth-$name.json
    code=$(curl_request "$body" \
        "$@" \
        --form-string recordedAt=1760000000500 \
        --form-string client=e2e-auth-check \
        --request POST "$base_url/webhook" || true)
    [ "$code" = 401 ] || die "webhook authentication failure returned the wrong status"
    python3 - "$body" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    if json.load(stream) != {"error": "unauthorized"}:
        raise SystemExit("invalid webhook authentication error")
PY
}
assert_webhook_auth_failure missing
assert_webhook_auth_failure invalid --header 'Authorization: Bearer invalid-e2e-token'

webhook_body=$diagnostics_dir/webhook.json
webhook_headers=$diagnostics_dir/webhook.headers
if ! webhook_code=$(curl_request "$webhook_body" \
    --dump-header "$webhook_headers" \
    --header "Authorization: Bearer $INDEX01_WEBHOOK_TOKEN" \
    --form-string recordedAt=1760000000000 \
    --form-string client=e2e-client \
    --form-string 'transcription=E2E synthetic transcription' \
    --request POST "$base_url/webhook"); then
    die "initial webhook request failed"
fi
[ "$webhook_code" = 202 ] || die "initial webhook did not return 202"

recording_id=$(python3 - "$webhook_body" <<'PY'
import json
import sys

try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        value = json.load(stream)
    if value.get("duplicate") is not False or value.get("queued") is not True:
        raise ValueError
    identifier = value.get("id")
    if not isinstance(identifier, int) or identifier <= 0:
        raise ValueError
except (OSError, ValueError, TypeError, json.JSONDecodeError):
    raise SystemExit("invalid webhook response")
print(identifier)
PY
) || die "initial webhook response was invalid"
case "$recording_id" in
    ''|*[!0-9]*) die "webhook identifier is invalid" ;;
esac

status_file=$diagnostics_dir/status.json
complete=0
for _ in $(seq 1 120); do
    if compose exec -T index-01-hook /index-01-hook status "$recording_id" >"$status_file" 2>&1; then
        if python3 - "$status_file" <<'PY'
import json
import sys

try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        value = json.load(stream)
    if value.get("state") != "complete":
        raise ValueError
except (OSError, ValueError, TypeError, json.JSONDecodeError):
    raise SystemExit(1)
PY
        then
            complete=1
            break
        fi
    fi
    sleep 1
done
[ "$complete" -eq 1 ] || die "recording did not complete"

python3 - "$status_file" <<'PY'
import json
import re
import sys

forbidden = {
    "E2E synthetic transcription",
    "E2E work task",
    "E2E task content",
    "synthetic-webhook-e2e-token-0123456789abcdef",
    "deepseek-e2e-token",
    "ticktick-e2e-token",
}
try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        value = json.load(stream)
    if value.get("provider") != "deepseek" or value.get("model") != "deepseek-v4-flash":
        raise ValueError
    if value.get("provider_response_id") != "deepseek-response-e2e-1":
        raise ValueError
    tasks = value.get("tasks")
    if not isinstance(tasks, list) or len(tasks) != 1:
        raise ValueError
    task = tasks[0]
    if task.get("state") != "complete" or task.get("ticktick_task_id") != "task-e2e-1" or task.get("ticktick_project_id") != "task-work":
        raise ValueError
    if not re.fullmatch(r"\[index01:[0-9a-f]{64}:0\]", task.get("marker", "")):
        raise ValueError
    encoded = json.dumps(value, sort_keys=True)
    if any(secret in encoded for secret in forbidden):
        raise ValueError
except (OSError, ValueError, TypeError, json.JSONDecodeError):
    raise SystemExit("unsafe or incomplete status")
PY

health_file=$diagnostics_dir/healthcheck.json
compose exec -T index-01-hook /index-01-hook healthcheck >"$health_file" 2>&1 || die "healthcheck failed"
python3 - "$health_file" <<'PY'
import json
import sys
try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        if json.load(stream) != {"status": "ok"}:
            raise ValueError
except (OSError, ValueError, TypeError, json.JSONDecodeError):
    raise SystemExit("invalid healthcheck output")
PY

projects_file=$diagnostics_dir/projects.json
compose exec -T index-01-hook /index-01-hook ticktick-projects >"$projects_file" 2>&1 || die "ticktick-projects failed"
python3 - "$projects_file" <<'PY'
import json
import sys

forbidden = {
    "Synthetic private default project",
    "Synthetic private work project",
    "Synthetic private note project",
    "synthetic-webhook-e2e-token-0123456789abcdef",
    "deepseek-e2e-token",
    "ticktick-e2e-token",
    "E2E synthetic transcription",
}
def strings(value):
    if isinstance(value, dict):
        for item in value.values():
            yield from strings(item)
    elif isinstance(value, list):
        for item in value:
            yield from strings(item)
    elif isinstance(value, str):
        yield value
try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        value = json.load(stream)
    if [item.get("id") for item in value] != ["note-list", "task-default", "task-work"]:
        raise ValueError
    if any(item.get("closed") is not False or item.get("writable") is not True for item in value):
        raise ValueError
    if {item.get("kind") for item in value} != {"TASK", "NOTE"}:
        raise ValueError
    if any(item == "write" or item in forbidden for item in strings(value)):
        raise ValueError
    if any("permission" in item for item in value):
        raise ValueError
except (OSError, ValueError, TypeError, json.JSONDecodeError):
    raise SystemExit("unsafe project output")
PY

if ! duplicate_code=$(curl_request "$diagnostics_dir/duplicate.json" \
    --header "Authorization: Bearer $INDEX01_WEBHOOK_TOKEN" \
    --form-string recordedAt=1760000000000 \
    --form-string client=e2e-client \
    --form-string 'transcription=E2E synthetic transcription' \
    --request POST "$base_url/webhook"); then
    die "duplicate webhook request failed"
fi
[ "$duplicate_code" = 200 ] || die "duplicate webhook did not return 200"
python3 - "$diagnostics_dir/duplicate.json" "$recording_id" <<'PY'
import json
import sys
try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        value = json.load(stream)
    if value != {"id": int(sys.argv[2]), "duplicate": True, "queued": False}:
        raise ValueError
except (OSError, ValueError, TypeError, json.JSONDecodeError):
    raise SystemExit("invalid duplicate response")
PY

if ! provider_free_code=$(curl_request "$diagnostics_dir/provider-free.json" \
    --header "Authorization: Bearer $INDEX01_WEBHOOK_TOKEN" \
    --form-string recordedAt=1760000001000 \
    --form-string client=e2e-provider-free \
    --request POST "$base_url/webhook"); then
    die "provider-free webhook request failed"
fi
[ "$provider_free_code" = 202 ] || die "provider-free webhook did not return 202"
python3 - "$diagnostics_dir/provider-free.json" <<'PY'
import json
import sys
try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        value = json.load(stream)
    if value.get("duplicate") is not False or value.get("queued") is not False or not isinstance(value.get("id"), int) or value["id"] <= 0:
        raise ValueError
except (OSError, ValueError, TypeError, json.JSONDecodeError):
    raise SystemExit("invalid provider-free response")
PY

python3 - "$event_dir/events.jsonl" <<'PY'
import sys
try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        if stream.read().splitlines() != ["deepseek", "ticktick-task"]:
            raise ValueError
except (OSError, ValueError):
    raise SystemExit("unexpected fixture events")
PY

ready_code=$(curl_request "$diagnostics_dir/ready-auth.json" \
    --header "Authorization: Bearer $INDEX01_WEBHOOK_TOKEN" \
    --request GET "$base_url/readyz" || true)
[ "$ready_code" = 200 ] || die "authenticated readiness failed"
missing_code=$(curl_request "$diagnostics_dir/ready-missing.json" \
    --request GET "$base_url/readyz" || true)
[ "$missing_code" = 401 ] || die "missing readiness authentication was accepted"
ready_query_code=$(curl_request "$diagnostics_dir/ready-query.json" \
    --header "Authorization: Bearer $INDEX01_WEBHOOK_TOKEN" \
    --request GET "$base_url/readyz?drift=1" || true)
[ "$ready_query_code" = 200 ] || die "readiness query behavior differs"
webhook_query_code=$(curl_request "$diagnostics_dir/webhook-query.json" \
    --form-string recordedAt=1760000000600 \
    --form-string client=e2e-query-check \
    --request POST "$base_url/webhook?drift=1" || true)
[ "$webhook_query_code" = 401 ] || die "webhook query bypassed authentication"

assert_status() {
    method=$1
    path=$2
    expected=$3
    body=$diagnostics_dir/route-$(printf '%s' "$method-$path" | tr '/?' '__').json
    actual=$(curl_request "$body" --request "$method" "$base_url$path" || true)
    [ "$actual" = "$expected" ] || die "unexpected route status"
}
assert_status GET /healthz 404
assert_status GET /statusz 404
assert_status GET /webhook 404
assert_status GET /readyz/ 404
assert_status GET /webhook/ 404
assert_status POST /webhook/ 404
assert_status GET /unmatched 404

python3 - "$webhook_headers" <<'PY'
import sys
try:
    with open(sys.argv[1], encoding="ascii") as stream:
        headers = stream.read().lower()
    if "cache-control: no-store" not in headers or "x-content-type-options: nosniff" not in headers:
        raise ValueError
except (OSError, ValueError):
    raise SystemExit("required security headers are missing")
PY

printf '%s\n' 'public experience E2E passed'
