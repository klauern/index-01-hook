#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/restore-external-backup-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
fake_bin=$test_root/bin
mkdir -p "$fake_bin"

image=registry.example/index-01-hook@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
backup_path=$test_root/index01-backup.db.age
checksum_path=$backup_path.sha256
identity_path=$test_root/age-identity.txt
manifest_path=$test_root/maintenance-pod.yaml
capture_path=$test_root/restored.db
digest=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
printf 'synthetic encrypted backup\n' >"$backup_path"
cp -f "$backup_path" "$test_root/original-backup.age"
printf '%s  %s\n' "$digest" "$(basename "$backup_path")" >"$checksum_path"
printf 'AGE-SECRET-KEY-TEST\n' >"$identity_path"
printf 'apiVersion: v1\nkind: Pod\n' >"$manifest_path"

cat >"$fake_bin/age" <<'EOF'
#!/bin/sh
set -eu
[ "${FAKE_AGE_FAIL:-}" != 1 ] || exit 24
if [ "${FAKE_MUTATE_SOURCE:-}" = 1 ]; then printf 'changed\n' >"$FAKE_BACKUP_PATH"; fi
cat
EOF
cat >"$fake_bin/shasum" <<'EOF'
#!/bin/sh
set -eu
[ "${FAKE_SHASUM_FAIL:-}" != 1 ] || exit 26
printf '%s  %s\n' "${FAKE_DIGEST}" "${3##*/}"
EOF
cat >"$fake_bin/kubectl" <<'EOF'
#!/bin/sh
set -eu
: "${FAKE_STATE_DIR:?}"
printf '%s\n' "$*" >>"$FAKE_STATE_DIR/kubectl.log"
if [ "${1:-}" = config ] && [ "${2:-}" = current-context ]; then
  printf '%s\n' test-context
  exit 0
fi
[ "${1:-}" = --context=test-context ] || exit 40
shift
call=$*
case " $call " in
  *" create --dry-run=client --validate=strict "*)
    case "$call" in
      *" -o name"*) printf '%s' "${FAKE_MANIFEST_RESOURCE:-pod/index-01-hook-maintenance}" ;;
      *metadata.namespace*) printf '%s' "${FAKE_MANIFEST_NAMESPACE:-index-01-hook}" ;;
      *spec.containers\[0\].image*) printf '%s' "${FAKE_MANIFEST_IMAGE:-$FAKE_EXPECTED_IMAGE}" ;;
      *) exit 39 ;;
    esac
    ;;
  *" create -f - "*)
    [ ! -f "$FAKE_STATE_DIR/lease" ] || exit 41
    manifest=$(cat)
    holder=$(printf '%s\n' "$manifest" | sed -n 's/.*"holderIdentity":"\([A-Za-z0-9._-]*\)".*/\1/p')
    [ -n "$holder" ] || exit 42
    printf '%s\n' "$holder" >"$FAKE_STATE_DIR/lease"
    ;;
  *" patch lease/index-01-hook-maintenance-lock "*)
    [ -f "$FAKE_STATE_DIR/lease" ] || exit 43
    patch=
    while [ "$#" -gt 0 ]; do
      if [ "$1" = -p ]; then patch=$2; break; fi
      shift
    done
    expected=$(printf '%s\n' "$patch" | sed -n 's/.*"op":"test"[^}]*"value":"\([A-Za-z0-9._-]*\)".*/\1/p')
    replacement=$(printf '%s\n' "$patch" | sed -n 's/.*"op":"replace"[^}]*"value":"\([A-Za-z0-9._-]*\)".*/\1/p')
    [ "$(cat "$FAKE_STATE_DIR/lease")" = "$expected" ] || exit 44
    printf '%s\n' "$replacement" >"$FAKE_STATE_DIR/lease"
    ;;
  *" get lease/index-01-hook-maintenance-lock "*)
    [ -f "$FAKE_STATE_DIR/lease" ] || exit 45
    cat "$FAKE_STATE_DIR/lease"
    ;;
  *" scale deployment/index-01-hook --replicas=0 "*)
    [ "${FAKE_SCALE_DOWN_FAIL:-}" != 1 ] || exit 46
    printf '0\n' >"$FAKE_STATE_DIR/replicas"
    ;;
  *" scale deployment/index-01-hook --replicas=1 "*)
    [ "${FAKE_SCALE_UP_FAIL:-}" != 1 ] || exit 47
    printf '1\n' >"$FAKE_STATE_DIR/replicas"
    ;;
  *" get pods "*)
    if [ -f "$FAKE_STATE_DIR/app-pods" ]; then cat "$FAKE_STATE_DIR/app-pods"; fi
    ;;
  *" wait --for=delete pod "*)
    [ "${FAKE_APP_WAIT_FAIL:-}" != 1 ] || exit 48
    rm -f "$FAKE_STATE_DIR/app-pods"
    ;;
  *" delete pod/index-01-hook-maintenance "*" --ignore-not-found=true "*)
    rm -f "$FAKE_STATE_DIR/maintenance"
    ;;
  *" apply --validate=strict -f "*)
    [ "${FAKE_APPLY_FAIL:-}" != 1 ] || exit 49
    printf 'present\n' >"$FAKE_STATE_DIR/maintenance"
    ;;
  *" wait --for=condition=Ready pod/index-01-hook-maintenance "*)
    [ "${FAKE_READY_WAIT_FAIL:-}" != 1 ] || exit 50
    [ -f "$FAKE_STATE_DIR/maintenance" ] || exit 51
    ;;
  *" get pvc/index-01-hook-data "*)
    count=0
    [ ! -f "$FAKE_STATE_DIR/pvc-count" ] || count=$(cat "$FAKE_STATE_DIR/pvc-count")
    count=$((count + 1))
    printf '%s\n' "$count" >"$FAKE_STATE_DIR/pvc-count"
    if [ "${FAKE_PVC_UID_CHANGE:-}" = 1 ] && [ "$count" -gt 1 ]; then
      printf 'changed-pvc-uid'
    else
      printf 'pvc-uid-1'
    fi
    ;;
  *" get pod/index-01-hook-maintenance "*)
    [ -f "$FAKE_STATE_DIR/maintenance" ] || exit 52
    case "$call" in
      *metadata.uid*) printf '%s' "${FAKE_POD_UID:-maintenance-uid-1}" ;;
      *status.phase*) printf '%s' "${FAKE_PHASE:-Running}" ;;
      *containerStatuses*) printf '%s' "${FAKE_READY:-true}" ;;
      *spec.containers\[0\].image*) printf '%s' "${FAKE_IMAGE:-$FAKE_EXPECTED_IMAGE}" ;;
      *command\[0\]*) printf '%s' "${FAKE_COMMAND0:-/index-01-hook}" ;;
      *command\[1\]*) printf '%s' "${FAKE_COMMAND1:-maintenance}" ;;
      *command\[2\]*) printf '%s' "${FAKE_COMMAND2:-}" ;;
      *INDEX01_DB_PATH*) printf '%s' "${FAKE_DB_PATH:-/var/lib/index-01-hook/data/index01.db}" ;;
      *persistentVolumeClaim.claimName*) printf '%s' "${FAKE_PVC_NAME:-index-01-hook-data}" ;;
      *volumeMounts*mountPath*) printf '%s' "${FAKE_MOUNT_PATH:-/var/lib/index-01-hook}" ;;
      *volumeMounts*readOnly*) printf '%s' "${FAKE_MOUNT_READ_ONLY:-false}" ;;
      *) exit 53 ;;
    esac
    ;;
  *" exec -i -n index-01-hook pod/index-01-hook-maintenance "*)
    cat >"$FAKE_RESTORE_CAPTURE"
    [ "${FAKE_RESTORE_FAIL:-}" != 1 ] || exit 54
    printf '%s\n' "${FAKE_RESTORE_OUTPUT:-{\"state\":\"restored\"}}"
    ;;
  *" delete pod/index-01-hook-maintenance "*" --wait=true "*)
    [ "${FAKE_FINAL_DELETE_FAIL:-}" != 1 ] || exit 55
    rm -f "$FAKE_STATE_DIR/maintenance"
    ;;
  *" rollout status deployment/index-01-hook "*)
    [ "${FAKE_ROLLOUT_FAIL:-}" != 1 ] || exit 56
    [ "$(cat "$FAKE_STATE_DIR/replicas")" = 1 ] || exit 57
    ;;
  *) exit 58 ;;
esac
EOF
chmod 0700 "$fake_bin/age" "$fake_bin/shasum" "$fake_bin/kubectl"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}
clear_overrides() {
  unset FAKE_AGE_FAIL FAKE_MUTATE_SOURCE FAKE_SHASUM_FAIL FAKE_SCALE_DOWN_FAIL
  unset FAKE_SCALE_UP_FAIL FAKE_APP_WAIT_FAIL FAKE_APPLY_FAIL FAKE_READY_WAIT_FAIL
  unset FAKE_PVC_UID_CHANGE FAKE_POD_UID FAKE_PHASE FAKE_READY FAKE_IMAGE
  unset FAKE_COMMAND0 FAKE_COMMAND1 FAKE_COMMAND2 FAKE_DB_PATH FAKE_PVC_NAME
  unset FAKE_MOUNT_PATH FAKE_MOUNT_READ_ONLY FAKE_RESTORE_FAIL FAKE_RESTORE_OUTPUT
  unset FAKE_FINAL_DELETE_FAIL FAKE_ROLLOUT_FAIL FAKE_PREEXISTING_LEASE FAKE_APP_PODS
  unset FAKE_MANIFEST_RESOURCE FAKE_MANIFEST_NAMESPACE FAKE_MANIFEST_IMAGE
}
run_restore() {
  state=$test_root/state
  rm -rf "$state"
  mkdir -p "$state"
  printf '1\n' >"$state/replicas"
  [ "${FAKE_PREEXISTING_LEASE:-}" != 1 ] || printf 'other-holder\n' >"$state/lease"
  [ -z "${FAKE_APP_PODS:-}" ] || printf '%s\n' "$FAKE_APP_PODS" >"$state/app-pods"
  rm -f "$capture_path"
  PATH="$fake_bin:$PATH" TMPDIR="$test_root" FAKE_STATE_DIR="$state" \
    FAKE_RESTORE_CAPTURE="$capture_path" FAKE_BACKUP_PATH="$backup_path" \
    FAKE_EXPECTED_IMAGE="$image" FAKE_DIGEST="$digest" \
    FAKE_AGE_FAIL="${FAKE_AGE_FAIL:-}" FAKE_MUTATE_SOURCE="${FAKE_MUTATE_SOURCE:-}" \
    FAKE_SHASUM_FAIL="${FAKE_SHASUM_FAIL:-}" FAKE_SCALE_DOWN_FAIL="${FAKE_SCALE_DOWN_FAIL:-}" \
    FAKE_SCALE_UP_FAIL="${FAKE_SCALE_UP_FAIL:-}" FAKE_APP_WAIT_FAIL="${FAKE_APP_WAIT_FAIL:-}" \
    FAKE_APPLY_FAIL="${FAKE_APPLY_FAIL:-}" FAKE_READY_WAIT_FAIL="${FAKE_READY_WAIT_FAIL:-}" \
    FAKE_PVC_UID_CHANGE="${FAKE_PVC_UID_CHANGE:-}" FAKE_POD_UID="${FAKE_POD_UID:-}" \
    FAKE_PHASE="${FAKE_PHASE:-}" FAKE_READY="${FAKE_READY:-}" FAKE_IMAGE="${FAKE_IMAGE:-}" \
    FAKE_COMMAND0="${FAKE_COMMAND0:-}" FAKE_COMMAND1="${FAKE_COMMAND1:-}" \
    FAKE_COMMAND2="${FAKE_COMMAND2:-}" FAKE_DB_PATH="${FAKE_DB_PATH:-}" \
    FAKE_PVC_NAME="${FAKE_PVC_NAME:-}" FAKE_MOUNT_PATH="${FAKE_MOUNT_PATH:-}" \
    FAKE_MOUNT_READ_ONLY="${FAKE_MOUNT_READ_ONLY:-}" FAKE_RESTORE_FAIL="${FAKE_RESTORE_FAIL:-}" \
    FAKE_RESTORE_OUTPUT="${FAKE_RESTORE_OUTPUT:-}" FAKE_FINAL_DELETE_FAIL="${FAKE_FINAL_DELETE_FAIL:-}" \
    FAKE_ROLLOUT_FAIL="${FAKE_ROLLOUT_FAIL:-}" FAKE_MANIFEST_RESOURCE="${FAKE_MANIFEST_RESOURCE:-}" \
    FAKE_MANIFEST_NAMESPACE="${FAKE_MANIFEST_NAMESPACE:-}" FAKE_MANIFEST_IMAGE="${FAKE_MANIFEST_IMAGE:-}" \
    "$project_dir/scripts/restore-external-backup.sh" "$backup_path" "$checksum_path" \
      "$identity_path" test-context index-01-hook "$image" "$manifest_path" \
      >"$test_root/stdout" 2>"$test_root/stderr"
}
expect_failure() {
  if run_restore; then fail "expected restore failure"; fi
  [ -f "$test_root/state/lease" ] || fail "failed restore did not retain the Lease"
  [ "$(cat "$test_root/state/lease")" != released ] || fail "failed restore released the Lease"
  clear_overrides
}
expect_prelock_failure() {
  if run_restore; then fail "expected pre-lock restore failure"; fi
  [ ! -f "$test_root/state/lease" ] || fail "pre-lock failure created a Lease"
  clear_overrides
}
expect_clean_temp() {
  if find "$test_root" -maxdepth 1 \( -name 'index01-restore.*' -o -name '.index01-restore-snapshot.*' -o -name '.index01-maintenance-snapshot.*' \) -print | grep -q .; then
    fail "restore left temporary data"
  fi
}

run_restore
cmp "$capture_path" "$backup_path" >/dev/null || fail "restore did not use the complete snapshot"
[ "$(cat "$test_root/state/replicas")" = 1 ] || fail "successful restore did not restart one replica"
[ ! -f "$test_root/state/maintenance" ] || fail "successful restore left maintenance Pod state"
[ "$(cat "$test_root/state/lease")" = released ] || fail "successful restore did not release the Lease"
expect_clean_temp

FAKE_APP_PODS=pod/index-01-hook-a run_restore
clear_overrides
FAKE_PREEXISTING_LEASE=1 expect_failure
FAKE_MANIFEST_RESOURCE=deployment.apps/unexpected expect_prelock_failure
FAKE_MANIFEST_NAMESPACE=other-namespace expect_prelock_failure
FAKE_MANIFEST_IMAGE=registry.example/other@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa expect_prelock_failure
FAKE_APP_PODS=pod/index-01-hook-a FAKE_APP_WAIT_FAIL=1 expect_failure
FAKE_IMAGE=registry.example/other@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa expect_failure
FAKE_MOUNT_PATH=/wrong expect_failure
FAKE_MOUNT_READ_ONLY=true expect_failure
FAKE_PVC_NAME=other-pvc expect_failure
FAKE_PVC_UID_CHANGE=1 expect_failure
FAKE_AGE_FAIL=1 expect_failure
FAKE_RESTORE_FAIL=1 expect_failure
FAKE_ROLLOUT_FAIL=1 expect_failure
[ "$(cat "$test_root/state/replicas")" = 0 ] || fail "rollout failure did not scale the application back to zero"
FAKE_MUTATE_SOURCE=1 run_restore
clear_overrides
cmp "$capture_path" "$test_root/original-backup.age" >/dev/null || fail "restore did not use the protected ciphertext copy"

ln -s "$backup_path" "$test_root/symlink.age"
if PATH="$fake_bin:$PATH" "$project_dir/scripts/restore-external-backup.sh" \
  "$test_root/symlink.age" "$checksum_path" "$identity_path" test-context index-01-hook \
  "$image" "$manifest_path" >/dev/null 2>&1; then
  fail "restore accepted a symbolic-link input"
fi

printf '%s\n' 'PASS: external encrypted backup restore workflow'
