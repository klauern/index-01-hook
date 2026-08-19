#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/backup-export-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
fake_bin=$test_root/bin
mkdir -p "$fake_bin"
real_ln=$(command -v ln)

cat >"$fake_bin/kubectl" <<'EOF'
#!/bin/sh
set -eu
if [ "${1:-}" = config ] && [ "${2:-}" = current-context ]; then
  printf '%s\n' test-context
  exit 0
fi
case " $* " in
  *" create -f - "*)
    [ ! -f "$FAKE_LEASE_STATE" ] || exit 30
    manifest=$(cat)
    holder=$(printf '%s\n' "$manifest" | sed -n 's/.*"holderIdentity":"\([A-Za-z0-9._-]*\)".*/\1/p')
    [ -n "$holder" ] || exit 31
    printf '%s\n' "$holder" >"$FAKE_LEASE_STATE"
    ;;
  *" patch lease/index-01-hook-maintenance-lock "*)
    [ -f "$FAKE_LEASE_STATE" ] || exit 32
    patch=
    while [ "$#" -gt 0 ]; do
      if [ "$1" = -p ]; then patch=$2; break; fi
      shift
    done
    expected=$(printf '%s\n' "$patch" | sed -n 's/.*"op":"test"[^}]*"value":"\([A-Za-z0-9._-]*\)".*/\1/p')
    replacement=$(printf '%s\n' "$patch" | sed -n 's/.*"op":"replace"[^}]*"value":"\([A-Za-z0-9._-]*\)".*/\1/p')
    [ "$(cat "$FAKE_LEASE_STATE")" = "$expected" ] || exit 33
    printf '%s\n' "$replacement" >"$FAKE_LEASE_STATE"
    ;;
  *" get deployment/index-01-hook "*)
    printf '%s' "${FAKE_REPLICA_STATUS:-1 1}"
    ;;
  *" exec "*)
    printf '%s' 'private plaintext backup'
    if [ "${FAKE_KUBECTL_FAIL:-}" = 1 ]; then
      exit 23
    fi
    ;;
  *)
    exit 25
    ;;
esac
EOF
cat >"$fake_bin/age" <<'EOF'
#!/bin/sh
set -eu
if [ "${FAKE_AGE_FAIL:-}" = 1 ]; then
  exit 24
fi
cat >/dev/null
printf 'encrypted backup artifact'
EOF
cat >"$fake_bin/shasum" <<'EOF'
#!/bin/sh
set -eu
if [ "${FAKE_SHASUM_FAIL:-}" = 1 ]; then
  exit 26
fi
if [ "${FAKE_SHASUM_INVALID:-}" = 1 ]; then
  printf 'not-a-digest  %s\n' "${2##*/}"
  exit 0
fi
printf '%s  %s\n' "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" "${2##*/}"
EOF
cat >"$fake_bin/ln" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "${3##*/}" >>"$FAKE_LN_LOG"
exec "$REAL_LN" "$@"
EOF
chmod 0700 "$fake_bin/kubectl" "$fake_bin/age" "$fake_bin/shasum" "$fake_bin/ln"

run_export() {
  destination=$1
  PATH="$fake_bin:$PATH" REAL_LN="$real_ln" FAKE_LN_LOG="$test_root/ln.log" \
    FAKE_LEASE_STATE="$test_root/lease" FAKE_REPLICA_STATUS="${FAKE_REPLICA_STATUS:-}" \
    FAKE_KUBECTL_FAIL="${FAKE_KUBECTL_FAIL:-}" FAKE_AGE_FAIL="${FAKE_AGE_FAIL:-}" \
    FAKE_SHASUM_FAIL="${FAKE_SHASUM_FAIL:-}" FAKE_SHASUM_INVALID="${FAKE_SHASUM_INVALID:-}" \
    "$project_dir/scripts/backup-export.sh" "$destination" age1recipient test-context index-01-hook \
    >"$test_root/stdout" 2>"$test_root/stderr"
}
expect_failure() {
  if run_export "$1"; then
    echo "FAIL: expected backup failure" >&2
    exit 1
  fi
}
assert_clean() {
  destination=$1
  if find "$(dirname "$destination")" -maxdepth 1 -name "$(basename "$destination")*" -print | grep -q .; then
    echo "FAIL: failed backup left an artifact" >&2
    exit 1
  fi
}

success=$test_root/success.db.age
run_export "$success"
grep -F 'private plaintext backup' "$success" >/dev/null && {
  echo "FAIL: backup published plaintext" >&2
  exit 1
}
grep -F 'encrypted backup artifact' "$success" >/dev/null || exit 1
[ -f "$success.sha256" ] || exit 1
[ "$(cat "$test_root/lease")" = released ] || { echo "FAIL: backup did not release the maintenance Lease" >&2; exit 1; }
[ "$(sed -n '1p' "$test_root/ln.log")" = success.db.age.sha256 ] || {
  echo "FAIL: checksum was not published first" >&2
  exit 1
}
[ "$(sed -n '2p' "$test_root/ln.log")" = success.db.age ] || exit 1

replica_failure=$test_root/replica-failure.db.age
FAKE_REPLICA_STATUS='2 1' expect_failure "$replica_failure"
assert_clean "$replica_failure"
ready_failure=$test_root/ready-failure.db.age
FAKE_REPLICA_STATUS='1 0' expect_failure "$ready_failure"
assert_clean "$ready_failure"

producer_failure=$test_root/producer-failure.db.age
FAKE_KUBECTL_FAIL=1 expect_failure "$producer_failure"
assert_clean "$producer_failure"
encryption_failure=$test_root/encryption-failure.db.age
FAKE_AGE_FAIL=1 expect_failure "$encryption_failure"
assert_clean "$encryption_failure"
checksum_failure=$test_root/checksum-failure.db.age
FAKE_SHASUM_FAIL=1 expect_failure "$checksum_failure"
assert_clean "$checksum_failure"
invalid_output=$test_root/invalid-output.db.age
FAKE_SHASUM_INVALID=1 expect_failure "$invalid_output"
assert_clean "$invalid_output"

existing_artifact=$test_root/existing.db.age
printf 'old artifact' >"$existing_artifact"
expect_failure "$existing_artifact"
[ "$(cat "$existing_artifact")" = 'old artifact' ] || exit 1
[ ! -e "$existing_artifact.sha256" ] || exit 1
existing_checksum=$test_root/checksum-existing.db.age
printf 'old checksum' >"$existing_checksum.sha256"
expect_failure "$existing_checksum"
[ "$(cat "$existing_checksum.sha256")" = 'old checksum' ] || exit 1
[ ! -e "$existing_checksum" ] || exit 1

unsafe=$test_root/unsafe-name.db\ name.age
expect_failure "$unsafe"
assert_clean "$unsafe"
traversal=$test_root/../unsafe-traversal.db.age
expect_failure "$traversal"
if PATH="$fake_bin:$PATH" FAKE_LEASE_STATE="$test_root/lease" \
  "$project_dir/scripts/backup-export.sh" "$test_root/wrong-namespace.age" age1recipient \
  test-context other-namespace >/dev/null 2>&1; then
  echo "FAIL: backup accepted a non-project namespace" >&2
  exit 1
fi

printf 'PASS: encrypted backup export workflow\n'
