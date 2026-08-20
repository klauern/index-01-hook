#!/bin/sh
# Validate the production image before running any Compose operation.
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
compose_env_file=${COMPOSE_ENV_FILE:-.env}

fail() {
	echo "Compose production guard: $*" >&2
	exit 1
}

[ -r "$compose_env_file" ] || fail "COMPOSE_ENV_FILE does not exist or is not readable"
command -v docker >/dev/null 2>&1 || fail "Docker is required"

# Ask Compose to resolve shell and env-file precedence. Do not source the file:
# Compose supports quoting and escaping rules that are not shell syntax.
if ! resolved_environment=$(docker compose --env-file "$compose_env_file" config --environment 2>/dev/null); then
	fail "could not resolve Compose environment"
fi
newline='
'
resolved_environment=${resolved_environment%"$newline"}
image_with_sentinel=$(printf '%s\n' "$resolved_environment" | awk -F= '
	$0 !~ /^[A-Za-z_][A-Za-z0-9_]*=/ { invalid = 1 }
	$1 == "INDEX01_IMAGE" {
		value = substr($0, index($0, "=") + 1)
		matches++
	}
	END {
		if (invalid || matches != 1) exit 1
		printf "%s", value
	}
'; printf '\037') || fail "INDEX01_IMAGE is required"
image=${image_with_sentinel%?}

"$script_dir/validate-release-inputs.sh" immutable-image "$image" INDEX01_IMAGE
exec docker compose --env-file "$compose_env_file" "$@"
