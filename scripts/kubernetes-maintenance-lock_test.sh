#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/kubernetes-maintenance-lock-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
fake_bin=$test_root/bin
state_path=$test_root/lease
log_path=$test_root/kubectl.log
mkdir -p "$fake_bin"

cat >"$fake_bin/kubectl" <<'EOF'
#!/bin/sh
set -eu
: "${FAKE_KUBECTL_LOG:?}"
printf '%s\n' "$*" >>"$FAKE_KUBECTL_LOG"
if [ "${1:-}" = config ] && [ "${2:-}" = current-context ]; then
  [ "${FAKE_CONFIG_FAIL:-}" != 1 ] || exit 20
  printf '%s\n' "${FAKE_CURRENT_CONTEXT:-test-context}"
  exit 0
fi
[ "${1:-}" = --context=test-context ] || exit 21
shift
case " $* " in
  *" create -f - "*)
    [ ! -f "$FAKE_LEASE_STATE" ] || exit 22
    manifest=$(cat)
    holder=$(printf '%s\n' "$manifest" | sed -n 's/.*"holderIdentity":"\([A-Za-z0-9._-]*\)".*/\1/p')
    [ -n "$holder" ] || exit 23
    printf '%s\n' "$holder" >"$FAKE_LEASE_STATE"
    ;;
  *" patch lease/index-01-hook-maintenance-lock "*)
    [ "${FAKE_PATCH_FAIL:-}" != 1 ] || exit 24
    [ -f "$FAKE_LEASE_STATE" ] || exit 25
    patch=
    while [ "$#" -gt 0 ]; do
      if [ "$1" = -p ]; then patch=$2; break; fi
      shift
    done
    [ -n "$patch" ] || exit 26
    expected=$(printf '%s\n' "$patch" | sed -n 's/.*"op":"test"[^}]*"value":"\([A-Za-z0-9._-]*\)".*/\1/p')
    replacement=$(printf '%s\n' "$patch" | sed -n 's/.*"op":"replace"[^}]*"value":"\([A-Za-z0-9._-]*\)".*/\1/p')
    [ -n "$expected" ] && [ -n "$replacement" ] || exit 27
    [ "$(cat "$FAKE_LEASE_STATE")" = "$expected" ] || exit 28
    printf '%s\n' "$replacement" >"$FAKE_LEASE_STATE"
    ;;
  *) exit 29 ;;
esac
EOF
chmod 0700 "$fake_bin/kubectl"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

run_lock() {
  PATH="$fake_bin:$PATH" FAKE_KUBECTL_LOG="$log_path" FAKE_LEASE_STATE="$state_path" \
    FAKE_CURRENT_CONTEXT="${FAKE_CURRENT_CONTEXT:-test-context}" \
    FAKE_CONFIG_FAIL="${FAKE_CONFIG_FAIL:-}" FAKE_PATCH_FAIL="${FAKE_PATCH_FAIL:-}" \
    "$project_dir/scripts/kubernetes-maintenance-lock.sh" "$@"
}
expect_failure() {
  if run_lock "$@" >"$test_root/lock.stdout" 2>"$test_root/lock.stderr"; then
    fail "expected lock operation to fail"
  fi
}
reset_case() {
  rm -f "$state_path" "$log_path"
  unset FAKE_CURRENT_CONTEXT FAKE_CONFIG_FAIL FAKE_PATCH_FAIL
}

reset_case
expect_failure acquire test-context index-01-hook released
run_lock acquire test-context index-01-hook deploy-4418
[ "$(cat "$state_path")" = deploy-4418 ] || fail "initial acquire failed"

run_lock release test-context index-01-hook deploy-4418
[ "$(cat "$state_path")" = released ] || fail "release did not preserve a released Lease"
grep -F 'patch lease/index-01-hook-maintenance-lock' "$log_path" >/dev/null || fail "release did not use a conditional patch"

run_lock acquire test-context index-01-hook restore-5529
[ "$(cat "$state_path")" = restore-5529 ] || fail "released Lease was not acquired"

expect_failure acquire test-context index-01-hook other-holder
[ "$(cat "$state_path")" = restore-5529 ] || fail "active Lease changed"
expect_failure release test-context index-01-hook wrong-holder
[ "$(cat "$state_path")" = restore-5529 ] || fail "wrong holder released the Lease"

FAKE_PATCH_FAIL=1 expect_failure release test-context index-01-hook restore-5529
[ "$(cat "$state_path")" = restore-5529 ] || fail "patch failure changed the Lease"
unset FAKE_PATCH_FAIL

FAKE_CURRENT_CONTEXT=other expect_failure acquire test-context index-01-hook another-holder
[ "$(cat "$state_path")" = restore-5529 ] || fail "context mismatch changed the Lease"
unset FAKE_CURRENT_CONTEXT

# A stale owner cannot release a Lease after another holder replaces its value.
printf '%s\n' replacement-holder >"$state_path"
expect_failure release test-context index-01-hook restore-5529
[ "$(cat "$state_path")" = replacement-holder ] || fail "compare-and-set release removed a replacement holder"

printf '%s\n' 'PASS: Kubernetes maintenance lock helper'
