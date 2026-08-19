#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/compose-operations-test.XXXXXX")
fake_bin=$test_root/bin
tmp_dir=$test_root/tmp
real_shasum=$(command -v shasum)
REAL_SHASUM=$real_shasum
export REAL_SHASUM
mkdir -p "$fake_bin" "$tmp_dir"
printf '%s\n' 'INDEX01_IMAGE=ghcr.io/example/index-01-hook@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' >"$test_root/.env"
export COMPOSE_ENV_FILE="$test_root/.env"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

cat >"$fake_bin/docker" <<'EOF'
#!/bin/sh
set -eu
: "${FAKE_DOCKER_LOG:?FAKE_DOCKER_LOG is required}"
printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
case " $* " in
	*" config --environment "*)
		printf 'INDEX01_IMAGE=%s\n' "${FAKE_INDEX01_IMAGE:-ghcr.io/example/index-01-hook@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}"
		;;
	*" exec -T index-01-hook /index-01-hook backup - "*)
		if [ "${FAKE_PRODUCER_FAIL:-}" = 1 ]; then exit 31; fi
		printf '%s\n' synthetic-database-backup
		;;
	*" stop index-01-hook "*)
		if [ "${FAKE_STOP_FAIL:-}" = 1 ]; then exit 32; fi
		printf '%s\n' stopped >"$FAKE_DOCKER_STATE"
		;;
	*" run --rm --no-deps -T --entrypoint /index-01-hook index-01-hook-maintenance restore - "*)
		cat >"$FAKE_RESTORE_CAPTURE"
		if [ "${FAKE_RESTORE_FAIL:-}" = 1 ]; then exit 33; fi
		if [ "${FAKE_RESTORE_OUTPUT_SET:-}" = 1 ]; then
			printf '%s\n' "${FAKE_RESTORE_OUTPUT:-}"
		else
			printf '%s\n' '{"state":"restored"}'
		fi
		;;
	*" run --rm --no-deps -T --entrypoint /index-01-hook index-01-hook restore - "*)
		echo "restore used the application service" >&2
		exit 36
		;;
	*" up -d --wait --wait-timeout 60 index-01-hook "*)
		if [ "${FAKE_WAIT_FAIL:-}" = 1 ] || [ "${FAKE_START_FAIL:-}" = 1 ]; then exit 34; fi
		printf '%s\n' started >>"$FAKE_DOCKER_STATE"
		;;
	*)
		echo "unexpected Docker command" >&2
		exit 35
		;;
esac
EOF
chmod 0755 "$fake_bin/docker"

cat >"$fake_bin/age" <<'EOF'
#!/bin/sh
set -eu
if [ "${1:-}" = -r ]; then
	[ "${FAKE_AGE_FAIL:-}" != 1 ] || exit 41
	output=
	while [ "$#" -gt 0 ]; do
		if [ "$1" = -o ]; then output=$2; shift 2; continue; fi
		shift
	done
	[ -n "$output" ] || exit 42
	cat >"$output"
	exit 0
fi
if [ "${FAKE_DECRYPT_FAIL:-}" = 1 ]; then exit 43; fi
if [ "${FAKE_MUTATE_RESTORE_SOURCE:-}" = 1 ]; then
	printf 'changed\n' >"$FAKE_RESTORE_SOURCE"
fi
cat
EOF
chmod 0755 "$fake_bin/age"

cat >"$fake_bin/shasum" <<'EOF'
#!/bin/sh
set -eu
[ "${FAKE_SHASUM_FAIL:-}" != 1 ] || exit 44
if [ "${FAKE_SHASUM_OUTPUT_SET:-}" = 1 ]; then
	printf '%s\n' "${FAKE_SHASUM_OUTPUT:-}"
	exit 0
fi
exec "$REAL_SHASUM" "$@"
EOF
chmod 0755 "$fake_bin/shasum"

run_backup() {
	PATH="$fake_bin:$PATH" FAKE_DOCKER_LOG="$test_root/docker.log" \
		FAKE_DOCKER_STATE="$test_root/docker.state" \
		"$project_dir/scripts/compose-backup.sh" age1synthetic "$1"
}

run_restore() {
	PATH="$fake_bin:$PATH" FAKE_DOCKER_LOG="$test_root/docker.log" \
		FAKE_DOCKER_STATE="$test_root/docker.state" FAKE_RESTORE_CAPTURE="$test_root/restore.capture" \
		FAKE_MUTATE_RESTORE_SOURCE="${FAKE_MUTATE_RESTORE_SOURCE:-}" FAKE_RESTORE_SOURCE="$1" \
		TMPDIR="$tmp_dir" "$project_dir/scripts/compose-restore.sh" \
		"$test_root/identity" "$1" "$1.sha256"
}

assert_no_restore_start() {
	if grep -F 'up -d --wait --wait-timeout 60 index-01-hook' "$test_root/docker.log" >/dev/null 2>&1; then
		fail "restore started the application after failure"
	fi
}

output=$test_root/backup.db.age
: >"$test_root/docker.log"
run_backup "$output" >/dev/null
[ -s "$output" ] || fail "backup output is missing"
[ -s "$output.sha256" ] || fail "backup checksum is missing"
first_backup=$(cat "$output")

if PATH="$fake_bin:$PATH" FAKE_DOCKER_LOG="$test_root/docker.log" \
	FAKE_DOCKER_STATE="$test_root/docker.state" FAKE_PRODUCER_FAIL=1 \
	"$project_dir/scripts/compose-backup.sh" age1synthetic "$test_root/producer-fail.age" \
	>/dev/null 2>&1; then
	fail "producer failure was accepted"
fi
[ ! -e "$test_root/producer-fail.age" ] || fail "producer failure published an artifact"
[ ! -e "$test_root/producer-fail.age.sha256" ] || fail "producer failure published a checksum"

if PATH="$fake_bin:$PATH" FAKE_DOCKER_LOG="$test_root/docker.log" \
	FAKE_DOCKER_STATE="$test_root/docker.state" FAKE_AGE_FAIL=1 \
	"$project_dir/scripts/compose-backup.sh" age1synthetic "$test_root/age-fail.age" \
	>/dev/null 2>&1; then
	fail "age failure was accepted"
fi
[ ! -e "$test_root/age-fail.age" ] || fail "age failure published an artifact"
[ ! -e "$test_root/age-fail.age.sha256" ] || fail "age failure published a checksum"

if PATH="$fake_bin:$PATH" FAKE_DOCKER_LOG="$test_root/docker.log" \
	FAKE_DOCKER_STATE="$test_root/docker.state" FAKE_SHASUM_FAIL=1 \
	"$project_dir/scripts/compose-backup.sh" age1synthetic "$test_root/shasum-fail.age" \
	>/dev/null 2>&1; then
	fail "shasum failure was accepted"
fi
[ ! -e "$test_root/shasum-fail.age" ] || fail "shasum failure published an artifact"
[ ! -e "$test_root/shasum-fail.age.sha256" ] || fail "shasum failure published a checksum"

if run_backup "$test_root/invalid name.age" >/dev/null 2>&1; then
	fail "invalid output basename was accepted"
fi
[ ! -e "$test_root/invalid name.age" ] || fail "invalid basename published an artifact"
[ ! -e "$test_root/invalid name.age.sha256" ] || fail "invalid basename published a checksum"

if PATH="$fake_bin:$PATH" FAKE_DOCKER_LOG="$test_root/docker.log" \
	FAKE_DOCKER_STATE="$test_root/docker.state" FAKE_SHASUM_OUTPUT_SET=1 FAKE_SHASUM_OUTPUT=invalid \
	"$project_dir/scripts/compose-backup.sh" age1synthetic "$test_root/invalid-shasum.age" \
	>/dev/null 2>&1; then
	fail "invalid shasum output was accepted"
fi
[ ! -e "$test_root/invalid-shasum.age" ] || fail "invalid shasum output published an artifact"
[ ! -e "$test_root/invalid-shasum.age.sha256" ] || fail "invalid shasum output published a checksum"

printf existing-artifact >"$test_root/artifact-fail.age"
if run_backup "$test_root/artifact-fail.age" >/dev/null 2>&1; then
	fail "artifact publication failure was accepted"
fi
[ ! -e "$test_root/artifact-fail.age.sha256" ] || fail "artifact publication failure left its checksum link"

if run_backup "$output" >/dev/null 2>&1; then
	fail "existing encrypted backup was overwritten"
fi
[ "$(cat "$output")" = "$first_backup" ] || fail "no-clobber changed the encrypted backup"

printf 'AGE-SECRET-KEY-synthetic\n' >"$test_root/identity"
: >"$test_root/docker.log"
if PATH="$fake_bin:$PATH" FAKE_DOCKER_LOG="$test_root/docker.log" \
	FAKE_DOCKER_STATE="$test_root/docker.state" \
	"$project_dir/scripts/compose-restore.sh" "$test_root/identity" \
	"$test_root/missing.age" "$test_root/missing.age.sha256" >/dev/null 2>&1; then
	fail "missing restore input was accepted"
fi
if grep -F 'stop index-01-hook' "$test_root/docker.log" >/dev/null 2>&1; then
	fail "early restore validation stopped the application"
fi
input=$test_root/input.age
printf 'synthetic-encrypted-backup\n' >"$input"
shasum -a 256 "$input" | awk '{print $1 "  input.age"}' >"$input.sha256"

: >"$test_root/docker.log"
printf '%s\n' old >"$test_root/docker.state"
printf '%064d  input.age\n' 0 >"$input.sha256"
if run_restore "$input" >/dev/null 2>&1; then
	fail "checksum failure was accepted"
fi
grep -F 'stop index-01-hook' "$test_root/docker.log" >/dev/null || fail "checksum failure did not stop the application"
assert_no_restore_start
[ ! -e "$test_root/restore.capture" ] || fail "checksum failure ran restore"

shasum -a 256 "$input" | awk '{print $1 "  input.age"}' >"$input.sha256"
cp -f "$input" "$test_root/input.original"
FAKE_MUTATE_RESTORE_SOURCE=1 run_restore "$input" >/dev/null
unset FAKE_MUTATE_RESTORE_SOURCE
[ "$(cat "$test_root/restore.capture")" = "$(cat "$test_root/input.original")" ] ||
	fail "restore did not use the protected encrypted snapshot"
cp -f "$test_root/input.original" "$input"
shasum -a 256 "$input" | awk '{print $1 "  input.age"}' >"$input.sha256"
: >"$test_root/docker.log"
if FAKE_DECRYPT_FAIL=1 run_restore "$input" >/dev/null 2>&1; then
	fail "decrypt failure was accepted"
fi
grep -F 'stop index-01-hook' "$test_root/docker.log" >/dev/null || fail "decrypt failure did not stop the application"
assert_no_restore_start
[ -z "$(find "$tmp_dir" -mindepth 1 -print -prune)" ] || fail "decrypt temporary data was not cleaned"
unset FAKE_DECRYPT_FAIL

: >"$test_root/docker.log"
if FAKE_RESTORE_FAIL=1 run_restore "$input" >/dev/null 2>&1; then
	fail "restore failure was accepted"
fi
grep -F 'stop index-01-hook' "$test_root/docker.log" >/dev/null || fail "restore failure did not stop the application"
assert_no_restore_start
[ -z "$(find "$tmp_dir" -mindepth 1 -print -prune)" ] || fail "restore temporary data was not cleaned"
unset FAKE_RESTORE_FAIL

for restore_output in '' '{"state":"wrong"}' '{"state":"restored"} trailing'; do
	: >"$test_root/docker.log"
	if FAKE_RESTORE_OUTPUT_SET=1 FAKE_RESTORE_OUTPUT="$restore_output" run_restore "$input" >/dev/null 2>&1; then
		fail "invalid restore JSON was accepted"
	fi
	assert_no_restore_start
	done
unset FAKE_RESTORE_OUTPUT_SET FAKE_RESTORE_OUTPUT

: >"$test_root/docker.log"
printf '%s\n' old >"$test_root/docker.state"
if FAKE_WAIT_FAIL=1 run_restore "$input" >/dev/null 2>&1; then
	fail "health-wait failure was accepted"
fi
[ "$(tail -n 1 "$test_root/docker.state")" = stopped ] || fail "health-wait failure left the application running"
grep -F 'up -d --wait --wait-timeout 60 index-01-hook' "$test_root/docker.log" >/dev/null || \
	fail "health-wait failure did not attempt the health wait"
unset FAKE_WAIT_FAIL

: >"$test_root/docker.log"
if FAKE_START_FAIL=1 run_restore "$input" >/dev/null 2>&1; then
	fail "application start failure was accepted"
fi
[ "$(grep -c 'stop index-01-hook' "$test_root/docker.log")" -ge 2 ] || \
	fail "application start failure did not leave the application stopped"
unset FAKE_START_FAIL

: >"$test_root/docker.log"
run_restore "$input" >/dev/null
grep -F 'stop index-01-hook' "$test_root/docker.log" >/dev/null || fail "successful restore did not stop the application"
grep -F 'run --rm --no-deps -T --entrypoint /index-01-hook index-01-hook-maintenance restore -' \
	"$test_root/docker.log" >/dev/null || fail "successful restore did not use the maintenance service"
if grep -F 'run --rm --no-deps -T --entrypoint /index-01-hook index-01-hook restore -' \
	"$test_root/docker.log" >/dev/null 2>&1; then
	fail "successful restore used the application service"
fi
grep -F 'up -d --wait --wait-timeout 60 index-01-hook' "$test_root/docker.log" >/dev/null || \
	fail "successful restore did not wait for application health"
[ "$(cat "$test_root/restore.capture")" = "$(cat "$input")" ] || fail "restore content changed"
[ -z "$(find "$tmp_dir" -mindepth 1 -print -prune)" ] || fail "successful restore temporary data was not cleaned"
[ -z "$(find "$test_root" -maxdepth 1 -name '.index-01-hook-restore-snapshot.*' -print -prune)" ] ||
	fail "Compose restore snapshot data was not cleaned"

echo "PASS: Compose backup and restore failure controls"
