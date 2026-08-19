#!/bin/sh
set -eu

fail() {
  echo "Kubernetes maintenance lock operation failed" >&2
  exit 1
}

[ "$#" -eq 4 ] || fail
operation=$1
expected_context=$2
namespace=$3
holder_identity=$4

case "$operation" in
  acquire|release) ;;
  *) fail ;;
esac
case "$expected_context" in
  ''|*[!A-Za-z0-9._:@/-]*) fail ;;
esac
[ "$namespace" = index-01-hook ] || fail
case "$holder_identity" in
  ''|released|*[!A-Za-z0-9._-]*) fail ;;
esac
[ "${#holder_identity}" -le 128 ] || fail

current_context=$(kubectl config current-context 2>/dev/null) || fail
[ "$current_context" = "$expected_context" ] || fail

lease_name=index-01-hook-maintenance-lock
if [ "$operation" = acquire ]; then
  lease_manifest=$(printf '%s\n' \
    '{"apiVersion":"coordination.k8s.io/v1","kind":"Lease","metadata":{"name":"index-01-hook-maintenance-lock","namespace":"index-01-hook"},"spec":{"holderIdentity":"'"$holder_identity"'"}}')
  if printf '%s\n' "$lease_manifest" | kubectl --context="$expected_context" create -f - \
    >/dev/null 2>&1; then
    exit 0
  fi
  acquire_patch='[{"op":"test","path":"/spec/holderIdentity","value":"released"},{"op":"replace","path":"/spec/holderIdentity","value":"'"$holder_identity"'"}]'
  kubectl --context="$expected_context" patch "lease/$lease_name" -n index-01-hook \
    --type=json -p "$acquire_patch" >/dev/null 2>&1 || fail
  exit 0
fi

release_patch='[{"op":"test","path":"/spec/holderIdentity","value":"'"$holder_identity"'"},{"op":"replace","path":"/spec/holderIdentity","value":"released"}]'
kubectl --context="$expected_context" patch "lease/$lease_name" -n index-01-hook \
  --type=json -p "$release_patch" >/dev/null 2>&1 || fail
