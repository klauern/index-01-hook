#!/bin/sh
# shellcheck disable=SC2016
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/compose-production-test.XXXXXX")
fake_bin=$test_root/bin
fake_log=$test_root/docker.log
env_file=$test_root/production.env
trap 'rm -rf -- "$test_root"' EXIT HUP INT TERM

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

mkdir -p "$fake_bin"
cat >"$fake_bin/docker" <<'EOF'
#!/bin/sh
set -eu
: "${FAKE_COMPOSE_ENV_FILE:?FAKE_COMPOSE_ENV_FILE is required}"
: "${FAKE_DOCKER_LOG:?FAKE_DOCKER_LOG is required}"
case " $* " in
  *" config --environment "*)
    if [ "${FAKE_CONFIG_FAIL:-}" = 1 ]; then
      printf 'INDEX01_IMAGE=%s\n' "$FAKE_IMAGE_OVERRIDE"
      exit 31
    fi
    if [ -n "${FAKE_IMAGE_OVERRIDE:-}" ]; then
      printf 'INDEX01_IMAGE=%s\n' "$FAKE_IMAGE_OVERRIDE"
    else
      awk -F= '$1 == "INDEX01_IMAGE" { print; found++ } END { if (found != 1) exit 1 }' "$FAKE_COMPOSE_ENV_FILE"
    fi
    ;;
  *)
    printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
    ;;
esac
EOF
chmod 0755 "$fake_bin/docker"

valid_digest=ghcr.io/example/index-01-hook@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa

expect_valid() {
	name=$1
	image=$2
	printf 'INDEX01_IMAGE=%s\n' "$image" >"$env_file"
	: >"$fake_log"
	if ! PATH="$fake_bin:$PATH" FAKE_COMPOSE_ENV_FILE="$env_file" \
		FAKE_DOCKER_LOG="$fake_log" FAKE_IMAGE_OVERRIDE="$image" COMPOSE_ENV_FILE="$env_file" \
		"$script_dir/compose-production.sh" up -d; then
		fail "rejected valid production image: $name"
	fi
	grep -F 'up -d' "$fake_log" >/dev/null || fail "did not run Compose for valid image: $name"
}

expect_invalid() {
	name=$1
	image=$2
	printf 'INDEX01_IMAGE=%s\n' "$image" >"$env_file"
	: >"$fake_log"
	if PATH="$fake_bin:$PATH" FAKE_COMPOSE_ENV_FILE="$env_file" \
		FAKE_DOCKER_LOG="$fake_log" FAKE_IMAGE_OVERRIDE="$image" COMPOSE_ENV_FILE="$env_file" \
		"$script_dir/compose-production.sh" up -d >"$test_root/$name.stdout" 2>"$test_root/$name.stderr"; then
		fail "accepted invalid production image: $name"
	fi
	[ ! -s "$fake_log" ] || fail "ran Compose for invalid image: $name"
}
expect_config_failure() {
	printf 'INDEX01_IMAGE=%s\n' "$valid_digest" >"$env_file"
	: >"$fake_log"
	if PATH="$fake_bin:$PATH" FAKE_COMPOSE_ENV_FILE="$env_file" FAKE_DOCKER_LOG="$fake_log" FAKE_IMAGE_OVERRIDE="$valid_digest" FAKE_CONFIG_FAIL=1 COMPOSE_ENV_FILE="$env_file" \
		"$script_dir/compose-production.sh" up -d >"$test_root/config-failure.stdout" 2>"$test_root/config-failure.stderr"; then
		fail "accepted a failed Compose environment resolution"
	fi
	[ ! -s "$fake_log" ] || fail "ran Compose after environment resolution failed"
}

expect_valid immutable-digest "$valid_digest"
expect_invalid tag ghcr.io/example/index-01-hook:latest
expect_invalid short-name index-01-hook
expect_invalid malformed-digest ghcr.io/example/index-01-hook@sha256:abc
expect_invalid control-character "ghcr.io/example/index-01-hook@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
bad"
expect_config_failure

printf '%s\n' 'PASS: production Compose image guard'
