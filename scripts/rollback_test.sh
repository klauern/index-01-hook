#!/bin/sh
# shellcheck disable=SC2016
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)
task_path=$(command -v task)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/rollback-test.XXXXXX")
fake_bin=$test_root/bin
log_path=$test_root/kubectl.log
lease_state=$test_root/lease
mkdir -p "$fake_bin"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

cat >"$fake_bin/kubectl" <<'FAKE_KUBECTL'
#!/bin/sh
set -eu

: "${FAKE_KUBECTL_LOG:?FAKE_KUBECTL_LOG is required}"
scenario=${FAKE_KUBECTL_SCENARIO:-success}

if [ "$#" -eq 2 ] && [ "$1" = config ] && [ "$2" = current-context ]; then
  printf '%s\n' test-context
  exit 0
fi

[ "${1:-}" = --context=test-context ] || {
  echo "fake kubectl rejected an absent or wrong context" >&2
  exit 96
}
shift
call=$*

case "$call" in
  *pvc*|*persistentvolumeclaim*|*secret*|*database*|*deepseek*|*ticktick*|*pebble*|*registry*|*dns*)
    echo "fake kubectl rejected a protected target: $call" >&2
    exit 98
    ;;
esac

case "$call" in
  "delete all "*|"delete namespace "*|*" --all"*)
    echo "fake kubectl rejected a broad delete: $call" >&2
    exit 98
    ;;
esac

log_call=$call
case "$call" in
  "patch lease/index-01-hook-maintenance-lock "*)
    case "$call" in
      *'"value":"released"}]') log_call='release maintenance Lease' ;;
      *) log_call='acquire released maintenance Lease' ;;
    esac
    ;;
esac
printf '%s\n' "$log_call" >>"$FAKE_KUBECTL_LOG"

case "$call" in
  "create -f -")
    if [ "$scenario" = lease-active ]; then
      printf '%s\n' other-holder >"$FAKE_LEASE_STATE"
      exit 30
    fi
    [ ! -f "$FAKE_LEASE_STATE" ] || exit 31
    manifest=$(cat)
    printf '%s\n' "$manifest" | sed -n 's/.*"holderIdentity":"\([A-Za-z0-9._-]*\)".*/\1/p' >"$FAKE_LEASE_STATE"
    [ -s "$FAKE_LEASE_STATE" ] || exit 32
    exit 0
    ;;
  "patch lease/index-01-hook-maintenance-lock "*)
    [ -f "$FAKE_LEASE_STATE" ] || exit 33
    patch=
    while [ "$#" -gt 0 ]; do
      if [ "$1" = -p ]; then patch=$2; break; fi
      shift
    done
    expected=$(printf '%s\n' "$patch" | sed -n 's/.*"op":"test"[^}]*"value":"\([A-Za-z0-9._-]*\)".*/\1/p')
    replacement=$(printf '%s\n' "$patch" | sed -n 's/.*"op":"replace"[^}]*"value":"\([A-Za-z0-9._-]*\)".*/\1/p')
    [ "$(cat "$FAKE_LEASE_STATE")" = "$expected" ] || exit 34
    if [ "$scenario" = lease-release-fail ] && [ "$replacement" = released ]; then exit 35; fi
    printf '%s\n' "$replacement" >"$FAKE_LEASE_STATE"
    exit 0
    ;;
esac

if [ "$#" -eq 6 ] && [ "$1" = rollout ] && [ "$2" = undo ] &&
  [ "$3" = deployment/index-01-hook ] && [ "$4" = -n ] &&
  [ "$5" = index-01-hook ] && [ "$6" = --to-revision=7 ]; then
    [ "$scenario" != rollback-undo-fail ] || exit 41
elif [ "$#" -eq 6 ] && [ "$1" = rollout ] && [ "$2" = status ] &&
  [ "$3" = deployment/index-01-hook ] && [ "$4" = -n ] &&
  [ "$5" = index-01-hook ] && [ "$6" = --timeout=180s ]; then
    [ "$scenario" != rollback-status-fail ] || exit 42
elif [ "$#" -eq 7 ] && [ "$1" = get ] &&
  [ "$2" = deployment/index-01-hook ] && [ "$3" = -n ] &&
  [ "$4" = index-01-hook ] && [ "$5" = --ignore-not-found=true ] &&
  [ "$6" = -o ] && [ "$7" = name ]; then
    [ "$scenario" != withdrawal-get-fail ] || exit 45
    case "$scenario" in
      withdrawal-absent|withdrawal-absent-delete-fail) ;;
      withdrawal-unexpected-get) printf '%s\n' service/index-01-hook ;;
      *) printf '%s\n' deployment.apps/index-01-hook ;;
    esac
elif [ "$#" -eq 5 ] && [ "$1" = scale ] &&
  [ "$2" = deployment/index-01-hook ] && [ "$3" = --replicas=0 ] &&
  [ "$4" = -n ] && [ "$5" = index-01-hook ]; then
    [ "$scenario" != withdrawal-scale-fail ] || exit 43
elif [ "$#" -eq 6 ] && [ "$1" = delete ] &&
  [ "$2" = service/index-01-hook ] && [ "$3" = ingress/index-01-hook ] &&
  [ "$4" = -n ] && [ "$5" = index-01-hook ] &&
  [ "$6" = --ignore-not-found=true ]; then
    case "$scenario" in
      withdrawal-delete-fail|withdrawal-absent-delete-fail) exit 44 ;;
    esac
else
  echo "fake kubectl rejected an unexpected call: $call" >&2
  exit 97
fi
FAKE_KUBECTL
chmod 0755 "$fake_bin/kubectl"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

reset_log() {
  : >"$log_path"
}

run_task() {
  scenario=$1
  shift
  /bin/rm -f "$lease_state"
  PATH="$fake_bin:$PATH" \
    FAKE_KUBECTL_LOG="$log_path" \
    FAKE_KUBECTL_SCENARIO="$scenario" \
    FAKE_LEASE_STATE="$lease_state" \
    "$task_path" --silent --yes --dir "$project_dir" "$@" KUBE_CONTEXT=test-context
}

expect_log() {
  expected=$1
  actual=$(cat "$log_path")
  [ "$actual" = "$expected" ] ||
    fail "kubectl calls differ\nexpected:\n$expected\nactual:\n$actual"
}

expect_task_failure() {
  scenario=$1
  shift
  if run_task "$scenario" "$@" >"$test_root/task.stdout" 2>"$test_root/task.stderr"; then
    fail "task succeeded: $*"
  fi
}

assert_rendered_tasks() {
  rollback_rendered=$test_root/rollback.rendered
  withdrawal_rendered=$test_root/withdrawal.rendered
  task --dry --yes --dir "$project_dir" rollback \
    REVISION=7 CONFIRM=rollback-to-revision-7 KUBE_CONTEXT=test-context >"$rollback_rendered" 2>&1
  task --dry --yes --dir "$project_dir" withdraw-first-deploy \
    CONFIRM=withdraw-first-deploy KUBE_CONTEXT=test-context >"$withdrawal_rendered" 2>&1

  for rendered in "$rollback_rendered" "$withdrawal_rendered"; do
    if grep -E '(^|[[:space:]])(curl|wget|rm|docker|gh|dig|nslookup|deepseek|ticktick|pebble)([[:space:]]|$)' \
      "$rendered" >/dev/null; then
      fail "rendered operations task contains an unsafe external command"
    fi
  done

  [ "$(grep -c '^[[:space:]]*kubectl ' "$rollback_rendered")" -eq 2 ] ||
    fail "rollback task does not contain exactly two kubectl commands"
  [ "$(grep -c '^[[:space:]]*kubectl ' "$withdrawal_rendered")" -eq 2 ] ||
    fail "withdrawal task does not contain exactly two direct kubectl commands"

  grep -F 'kubectl --context="$kube_context" rollout undo deployment/index-01-hook -n index-01-hook' \
    "$rollback_rendered" >/dev/null || fail "rollback undo command differs"
  grep -F 'kubectl --context="$kube_context" rollout status deployment/index-01-hook -n index-01-hook' \
    "$rollback_rendered" >/dev/null || fail "rollback status command differs"
  grep -F 'deployment_name="$(kubectl --context="$kube_context" get deployment/index-01-hook' \
    "$withdrawal_rendered" >/dev/null || fail "withdrawal get command differs"
  grep -F 'kubectl --context="$kube_context" scale deployment/index-01-hook --replicas=0' \
    "$withdrawal_rendered" >/dev/null || fail "withdrawal scale command differs"
  grep -F 'kubectl --context="$kube_context" delete service/index-01-hook ingress/index-01-hook' \
    "$withdrawal_rendered" >/dev/null || fail "withdrawal delete command differs"
}

assert_rendered_tasks

reset_log
run_task success rollback REVISION=7 CONFIRM=rollback-to-revision-7
expect_log 'create -f -
rollout undo deployment/index-01-hook -n index-01-hook --to-revision=7
rollout status deployment/index-01-hook -n index-01-hook --timeout=180s
release maintenance Lease'

reset_log
expect_task_failure lease-release-fail rollback REVISION=7 CONFIRM=rollback-to-revision-7
expect_log 'create -f -
rollout undo deployment/index-01-hook -n index-01-hook --to-revision=7
rollout status deployment/index-01-hook -n index-01-hook --timeout=180s
release maintenance Lease'
[ -f "$lease_state" ] || fail "release failure did not leave the Lease"

reset_log
expect_task_failure rollback-undo-fail rollback REVISION=7 CONFIRM=rollback-to-revision-7
expect_log 'create -f -
rollout undo deployment/index-01-hook -n index-01-hook --to-revision=7
release maintenance Lease'

reset_log
expect_task_failure rollback-status-fail rollback REVISION=7 CONFIRM=rollback-to-revision-7
expect_log 'create -f -
rollout undo deployment/index-01-hook -n index-01-hook --to-revision=7
rollout status deployment/index-01-hook -n index-01-hook --timeout=180s
release maintenance Lease'

reset_log
expect_task_failure lease-active rollback REVISION=7 CONFIRM=rollback-to-revision-7
expect_log 'create -f -
acquire released maintenance Lease'

for revision in 0 07 invalid '7;delete'; do
  reset_log
  expect_task_failure success rollback REVISION="$revision" CONFIRM="rollback-to-revision-$revision"
  expect_log ''
done

reset_log
expect_task_failure success rollback REVISION=7 CONFIRM=rollback-to-revision-8
expect_log ''

reset_log
expect_task_failure success rollback CONFIRM=rollback-to-revision-7
expect_log ''

reset_log
expect_task_failure success rollback REVISION=7
expect_log ''

reset_log
expect_task_failure lease-active withdraw-first-deploy CONFIRM=withdraw-first-deploy
expect_log 'create -f -
acquire released maintenance Lease'

reset_log
run_task success withdraw-first-deploy CONFIRM=withdraw-first-deploy
expect_log 'create -f -
get deployment/index-01-hook -n index-01-hook --ignore-not-found=true -o name
scale deployment/index-01-hook --replicas=0 -n index-01-hook
delete service/index-01-hook ingress/index-01-hook -n index-01-hook --ignore-not-found=true
release maintenance Lease'

reset_log
expect_task_failure withdrawal-scale-fail withdraw-first-deploy CONFIRM=withdraw-first-deploy
expect_log 'create -f -
get deployment/index-01-hook -n index-01-hook --ignore-not-found=true -o name
scale deployment/index-01-hook --replicas=0 -n index-01-hook
release maintenance Lease'

reset_log
expect_task_failure withdrawal-get-fail withdraw-first-deploy CONFIRM=withdraw-first-deploy
expect_log 'create -f -
get deployment/index-01-hook -n index-01-hook --ignore-not-found=true -o name
release maintenance Lease'

reset_log
expect_task_failure withdrawal-unexpected-get withdraw-first-deploy CONFIRM=withdraw-first-deploy
expect_log 'create -f -
get deployment/index-01-hook -n index-01-hook --ignore-not-found=true -o name
release maintenance Lease'

reset_log
expect_task_failure withdrawal-delete-fail withdraw-first-deploy CONFIRM=withdraw-first-deploy
expect_log 'create -f -
get deployment/index-01-hook -n index-01-hook --ignore-not-found=true -o name
scale deployment/index-01-hook --replicas=0 -n index-01-hook
delete service/index-01-hook ingress/index-01-hook -n index-01-hook --ignore-not-found=true
release maintenance Lease'

reset_log
run_task withdrawal-absent withdraw-first-deploy CONFIRM=withdraw-first-deploy
expect_log 'create -f -
get deployment/index-01-hook -n index-01-hook --ignore-not-found=true -o name
delete service/index-01-hook ingress/index-01-hook -n index-01-hook --ignore-not-found=true
release maintenance Lease'

reset_log
expect_task_failure withdrawal-absent-delete-fail withdraw-first-deploy CONFIRM=withdraw-first-deploy
expect_log 'create -f -
get deployment/index-01-hook -n index-01-hook --ignore-not-found=true -o name
delete service/index-01-hook ingress/index-01-hook -n index-01-hook --ignore-not-found=true
release maintenance Lease'

reset_log
expect_task_failure success withdraw-first-deploy CONFIRM=rollback-to-revision-7
expect_log ''

reset_log
expect_task_failure success withdraw-first-deploy
expect_log ''

expect_fake_reject() {
  if FAKE_KUBECTL_LOG="$log_path" "$fake_bin/kubectl" --context=test-context "$@" \
    >"$test_root/fake.stdout" 2>"$test_root/fake.stderr"; then
    fail "fake kubectl accepted a forbidden call: $*"
  fi
}

expect_fake_reject delete pvc/index-01-hook-data -n index-01-hook
expect_fake_reject delete secret/index-01-hook-secrets -n index-01-hook
expect_fake_reject delete database/index-01-hook -n index-01-hook
expect_fake_reject delete provider/deepseek -n index-01-hook
expect_fake_reject delete registry/ghcr -n index-01-hook
expect_fake_reject delete dns/hook.example.com -n index-01-hook
expect_fake_reject delete all -n index-01-hook
expect_fake_reject get secret/index-01-hook-secrets -n index-01-hook
expect_fake_reject describe deployment/index-01-hook -n index-01-hook
expect_fake_reject 'delete service/index-01-hook ingress/index-01-hook -n index-01-hook --ignore-not-found=true'

echo "PASS: deterministic rollback and first-deploy withdrawal scope"
