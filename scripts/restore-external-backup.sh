#!/bin/sh
set -eu

fail() {
  echo "external backup restore failed" >&2
  exit 1
}

[ "$#" -eq 7 ] || fail
backup_path=$1
checksum_path=$2
age_identity=$3
kube_context=$4
namespace=$5
expected_image=$6
maintenance_manifest=$7

for path in "$backup_path" "$checksum_path" "$age_identity" "$maintenance_manifest"; do
  case "$path" in
    /*) ;;
    *) fail ;;
  esac
  case "$path" in
    */../*|*/./*) fail ;;
  esac
  case "$(basename "$path" 2>/dev/null)" in
    ''|.|..|*[!A-Za-z0-9._-]*) fail ;;
  esac
  [ ! -L "$path" ] || fail
  [ -f "$path" ] || fail
done
case "$backup_path" in
  *.age) ;;
  *) fail ;;
esac
[ "$checksum_path" = "$backup_path.sha256" ] || fail
case "$maintenance_manifest" in
  */maintenance-pod.yaml) ;;
  *) fail ;;
esac
[ "$namespace" = index-01-hook ] || fail

script_dir=$(CDPATH='' cd "$(dirname "$0" 2>/dev/null)" && pwd) || fail
"$script_dir/validate-release-inputs.sh" immutable-image "$expected_image" EXPECTED_IMAGE \
  >/dev/null 2>&1 || fail
for command_name in age awk basename chmod cp dirname kubectl ln mktemp rm shasum; do
  command -v "$command_name" >/dev/null 2>&1 || fail
done

# Copy encrypted inputs before checksum validation to prevent pathname replacement.
snapshot_dir=
manifest_snapshot_dir=
temporary_dir=
backup_copy=
checksum_copy=
plaintext_file=
lease_created=0
workload_stopped=0
holder_identity=
cleanup() {
  status=$?
  if [ "$status" -ne 0 ] && [ "${lease_created:-0}" = 1 ] && [ "${workload_stopped:-0}" = 1 ]; then
    kubectl --context="$kube_context" scale deployment/index-01-hook --replicas=0 \
      -n index-01-hook >/dev/null 2>&1 || :
    cleanup_pods=$(kubectl --context="$kube_context" get pods -n index-01-hook \
      -l app.kubernetes.io/name=index-01-hook -o name 2>/dev/null) || cleanup_pods=unknown
    if [ -n "$cleanup_pods" ] && [ "$cleanup_pods" != unknown ]; then
      kubectl --context="$kube_context" wait --for=delete pod -n index-01-hook \
        -l app.kubernetes.io/name=index-01-hook --timeout=180s >/dev/null 2>&1 || :
    fi
  fi
  if [ "$status" -eq 0 ] && [ "${lease_created:-0}" = 1 ] && [ -n "${holder_identity:-}" ]; then
    if ! "$script_dir/kubernetes-maintenance-lock.sh" release "$kube_context" index-01-hook \
      "$holder_identity"; then
      echo "external backup restore failed" >&2
      status=1
    fi
  fi
  if [ -n "${temporary_dir:-}" ]; then
    rm -rf -- "$temporary_dir" >/dev/null 2>&1 || :
  fi
  if [ -n "${snapshot_dir:-}" ]; then
    rm -rf -- "$snapshot_dir" >/dev/null 2>&1 || :
  fi
  if [ -n "${manifest_snapshot_dir:-}" ]; then
    rm -rf -- "$manifest_snapshot_dir" >/dev/null 2>&1 || :
  fi
  return "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

backup_dir=$(dirname "$backup_path") || fail
[ "$(dirname "$checksum_path")" = "$backup_dir" ] || fail
snapshot_dir=$(mktemp -d "$backup_dir/.index01-restore-snapshot.XXXXXX" 2>/dev/null) || fail
chmod 0700 "$snapshot_dir" >/dev/null 2>&1 || fail
ln -h "$backup_path" "$snapshot_dir/backup.age" >/dev/null 2>&1 || fail
ln -h "$checksum_path" "$snapshot_dir/backup.sha256" >/dev/null 2>&1 || fail
[ ! -L "$snapshot_dir/backup.age" ] && [ ! -L "$snapshot_dir/backup.sha256" ] || fail
manifest_dir=$(dirname "$maintenance_manifest") || fail
manifest_snapshot_dir=$(mktemp -d "$manifest_dir/.index01-maintenance-snapshot.XXXXXX" 2>/dev/null) || fail
chmod 0700 "$manifest_snapshot_dir" >/dev/null 2>&1 || fail
ln -h "$maintenance_manifest" "$manifest_snapshot_dir/maintenance-pod.yaml" >/dev/null 2>&1 || fail
[ ! -L "$manifest_snapshot_dir/maintenance-pod.yaml" ] || fail

# Copy from protected hard links. Checksum validation detects in-place changes.
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/index01-restore.XXXXXX" 2>/dev/null) || fail
chmod 0700 "$temporary_dir" >/dev/null 2>&1 || fail
backup_copy=$temporary_dir/backup.age
checksum_copy=$temporary_dir/backup.sha256
maintenance_copy=$temporary_dir/maintenance-pod.yaml
cp -f -- "$snapshot_dir/backup.age" "$backup_copy" >/dev/null 2>&1 || fail
cp -f -- "$snapshot_dir/backup.sha256" "$checksum_copy" >/dev/null 2>&1 || fail
cp -f -- "$manifest_snapshot_dir/maintenance-pod.yaml" "$maintenance_copy" >/dev/null 2>&1 || fail
chmod 0600 "$backup_copy" "$checksum_copy" "$maintenance_copy" >/dev/null 2>&1 || fail

"$script_dir/require-kube-context.sh" "$kube_context" >/dev/null 2>&1 || fail
manifest_resource=$(kubectl --context="$kube_context" create --dry-run=client --validate=strict \
  -f "$maintenance_copy" -o name 2>/dev/null) || fail
[ "$manifest_resource" = pod/index-01-hook-maintenance ] || fail
manifest_namespace=$(kubectl --context="$kube_context" create --dry-run=client --validate=strict \
  -f "$maintenance_copy" -o jsonpath='{.metadata.namespace}' 2>/dev/null) || fail
[ "$manifest_namespace" = index-01-hook ] || fail
manifest_image=$(kubectl --context="$kube_context" create --dry-run=client --validate=strict \
  -f "$maintenance_copy" -o jsonpath='{.spec.containers[0].image}' 2>/dev/null) || fail
[ "$manifest_image" = "$expected_image" ] || fail
holder_identity="index-01-hook-restore-$(basename "$temporary_dir")"
"$script_dir/kubernetes-maintenance-lock.sh" acquire "$kube_context" index-01-hook \
  "$holder_identity" || fail
lease_created=1

backup_name=$(basename "$backup_path" 2>/dev/null) || fail
checksum_record=$(awk -v name="$backup_name" '
  NF == 2 && length($1) == 64 && $1 !~ /[^0-9a-f]/ && $2 == name {
    checksum = $1
    valid++
  }
  END {
    if (NR != 1 || valid != 1) exit 1
    print checksum
  }
' "$checksum_copy" 2>/dev/null) || fail
actual_checksum_output=$(shasum -a 256 "$backup_copy" 2>/dev/null) || fail
actual_checksum=$(printf '%s\n' "$actual_checksum_output" | awk '
  NF == 2 && length($1) == 64 && $1 !~ /[^0-9a-f]/ { checksum = $1; valid++ }
  END { if (NR != 1 || valid != 1) exit 1; print checksum }
') || fail
[ "$actual_checksum" = "$checksum_record" ] || fail

# The restore process owns workload shutdown and maintenance Pod creation.
kubectl --context="$kube_context" scale deployment/index-01-hook --replicas=0 \
  -n index-01-hook >/dev/null 2>&1 || fail
workload_stopped=1
application_label=app.kubernetes.io/name=index-01-hook
application_pods=$(kubectl --context="$kube_context" get pods -n index-01-hook \
  -l "$application_label" -o name 2>/dev/null) || fail
if [ -n "$application_pods" ]; then
  kubectl --context="$kube_context" wait --for=delete pod -n index-01-hook \
    -l "$application_label" --timeout=180s >/dev/null 2>&1 || fail
fi
application_pods=$(kubectl --context="$kube_context" get pods -n index-01-hook \
  -l "$application_label" -o name 2>/dev/null) || fail
[ -z "$application_pods" ] || fail

kubectl --context="$kube_context" delete pod/index-01-hook-maintenance -n index-01-hook \
  --ignore-not-found=true --wait=true >/dev/null 2>&1 || fail
kubectl --context="$kube_context" apply --validate=strict -f "$maintenance_copy" \
  >/dev/null 2>&1 || fail
kubectl --context="$kube_context" wait --for=condition=Ready \
  pod/index-01-hook-maintenance -n index-01-hook --timeout=120s >/dev/null 2>&1 || fail

expected_uid=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance \
  -n index-01-hook -o jsonpath='{.metadata.uid}' 2>/dev/null) || fail
case "$expected_uid" in
  ''|*[!a-z0-9-]*) fail ;;
esac
expected_pvc_uid=$(kubectl --context="$kube_context" get pvc/index-01-hook-data \
  -n index-01-hook -o jsonpath='{.metadata.uid}' 2>/dev/null) || fail
case "$expected_pvc_uid" in
  ''|*[!a-z0-9-]*) fail ;;
esac

check_maintenance_pod() {
  pod_uid=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.metadata.uid}' 2>/dev/null) || fail
  phase=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.status.phase}' 2>/dev/null) || fail
  ready=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.status.containerStatuses[0].ready}' 2>/dev/null) || fail
  image=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.spec.containers[0].image}' 2>/dev/null) || fail
  command0=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.spec.containers[0].command[0]}' 2>/dev/null) || fail
  command1=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.spec.containers[0].command[1]}' 2>/dev/null) || fail
  command2=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.spec.containers[0].command[2]}' 2>/dev/null) || fail
  db_path=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.spec.containers[0].env[?(@.name=="INDEX01_DB_PATH")].value}' 2>/dev/null) || fail
  pvc_name=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.spec.volumes[?(@.name=="data")].persistentVolumeClaim.claimName}' 2>/dev/null) || fail
  mount_path=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.spec.containers[0].volumeMounts[?(@.name=="data")].mountPath}' 2>/dev/null) || fail
  mount_read_only=$(kubectl --context="$kube_context" get pod/index-01-hook-maintenance -n index-01-hook \
    -o jsonpath='{.spec.containers[0].volumeMounts[?(@.name=="data")].readOnly}' 2>/dev/null) || fail
  pvc_uid=$(kubectl --context="$kube_context" get pvc/index-01-hook-data -n index-01-hook \
    -o jsonpath='{.metadata.uid}' 2>/dev/null) || fail
  [ "$pod_uid" = "$expected_uid" ] || fail
  [ "$phase" = Running ] || fail
  [ "$ready" = true ] || fail
  [ "$image" = "$expected_image" ] || fail
  [ "$command0" = /index-01-hook ] || fail
  [ "$command1" = maintenance ] || fail
  [ -z "$command2" ] || fail
  [ "$db_path" = /var/lib/index-01-hook/data/index01.db ] || fail
  [ "$pvc_name" = index-01-hook-data ] || fail
  [ "$mount_path" = /var/lib/index-01-hook ] || fail
  case "$mount_read_only" in ''|false) ;; *) fail ;; esac
  [ "$pvc_uid" = "$expected_pvc_uid" ] || fail
}

check_lock_and_application() {
  lease_holder=$(kubectl --context="$kube_context" get lease/index-01-hook-maintenance-lock \
    -n index-01-hook -o jsonpath='{.spec.holderIdentity}' 2>/dev/null) || fail
  [ "$lease_holder" = "$holder_identity" ] || fail
  pods=$(kubectl --context="$kube_context" get pods -n index-01-hook \
    -l "$application_label" -o name 2>/dev/null) || fail
  [ -z "$pods" ] || fail
}

check_maintenance_pod
check_lock_and_application
plaintext_file=$temporary_dir/restore.db
: >"$plaintext_file" || fail
chmod 0600 "$plaintext_file" >/dev/null 2>&1 || fail
age --decrypt --identity "$age_identity" <"$backup_copy" >"$plaintext_file" 2>/dev/null || fail

check_maintenance_pod
check_lock_and_application
restore_output=$(kubectl --context="$kube_context" exec -i -n index-01-hook \
  pod/index-01-hook-maintenance -- /index-01-hook restore - <"$plaintext_file" 2>/dev/null) || fail
[ "$restore_output" = '{"state":"restored"}' ] || fail

# Keep the Lease until the maintenance Pod is gone and the application is healthy.
check_maintenance_pod
check_lock_and_application
kubectl --context="$kube_context" delete pod/index-01-hook-maintenance -n index-01-hook \
  --wait=true >/dev/null 2>&1 || fail
kubectl --context="$kube_context" scale deployment/index-01-hook --replicas=1 \
  -n index-01-hook >/dev/null 2>&1 || fail
kubectl --context="$kube_context" rollout status deployment/index-01-hook \
  -n index-01-hook --timeout=180s >/dev/null 2>&1 || fail
workload_stopped=0

printf 'Restored encrypted backup SHA-256: %s\n' "$actual_checksum"
