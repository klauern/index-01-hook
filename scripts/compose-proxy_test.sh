#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)
config=$project_dir/deploy/compose/Caddyfile.example

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

[ -f "$config" ] || fail "Caddy example is missing"
[ "$(grep -c '^[[:space:]]*method POST$' "$config")" -eq 1 ] ||
	fail "Caddy must define one POST matcher"
[ "$(grep -c '^[[:space:]]*path /webhook$' "$config")" -eq 1 ] ||
	fail "Caddy must expose only the exact webhook path"
[ "$(grep -c '^[[:space:]]*method GET$' "$config")" -eq 1 ] ||
	fail "Caddy must define one GET matcher"
[ "$(grep -c '^[[:space:]]*path /readyz$' "$config")" -eq 1 ] ||
	fail "Caddy must expose only the exact readiness path"
[ "$(grep -c '^[[:space:]]*reverse_proxy 127\.0\.0\.1:8080$' "$config")" -eq 2 ] ||
	fail "Caddy upstreams must use the private loopback port"
[ "$(grep -c '^[[:space:]]*respond 404$' "$config")" -eq 1 ] ||
	fail "Caddy must reject unmatched public requests"

if grep -E 'path .*[?*]|/(healthz|statusz)' "$config" >/dev/null 2>&1; then
	fail "Caddy example contains a wildcard or private endpoint"
fi

if command -v caddy >/dev/null 2>&1; then
	caddy validate --config "$config" >/dev/null
fi

printf 'PASS: Caddy exposes only the supported exact public routes\n'
