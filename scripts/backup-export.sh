#!/bin/sh
set -eu

# Portable replacement for BSD "ln -h" (GNU coreutils ln has no -h flag):
# refuse an existing destination, create the hard link, then verify the
# destination is not a symbolic link.
link_no_follow() {
  if [ -e "$2" ] || [ -L "$2" ]; then
    return 1
  fi
  ln -- "$1" "$2" || return 1
  [ ! -L "$2" ]
}

fail() {
  echo "encrypted backup export failed" >&2
  exit 1
}

[ "$#" -eq 4 ] || fail
destination=$1
age_recipient=$2
kube_context=$3
namespace=$4

case "$destination" in
  /*) ;;
  *) fail ;;
esac
case "$destination" in
  */../*|*/./*) fail ;;
esac
destination_name=$(basename "$destination")
case "$destination_name" in
  ''|.|..|*[!A-Za-z0-9._-]*) fail ;;
esac
case "$destination_name" in
  *.age) ;;
  *) fail ;;
esac
[ -n "$age_recipient" ] || fail
[ "$namespace" = index-01-hook ] || fail

for command_name in age awk basename chmod dirname kubectl ln mkfifo mktemp rm rmdir shasum; do
  command -v "$command_name" >/dev/null 2>&1 || fail
done

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
"$script_dir/require-kube-context.sh" "$kube_context" || fail

partial=${destination}.partial
pipe_path=${partial}.pipe
checksum_path=${destination}.sha256
partial_checksum=${checksum_path}.partial
owned_partial=false
owned_pipe=false
owned_partial_checksum=false
checksum_published=false
artifact_published=false
producer_pid=
age_pid=
lease_created=false
holder_identity=

cleanup() {
  status=$?
  if [ -n "$producer_pid" ]; then
    kill "$producer_pid" 2>/dev/null || true
    wait "$producer_pid" 2>/dev/null || true
  fi
  if [ -n "$age_pid" ]; then
    kill "$age_pid" 2>/dev/null || true
    wait "$age_pid" 2>/dev/null || true
  fi
  if [ "$owned_pipe" = true ]; then rm -f -- "$pipe_path"; fi
  if [ "$owned_partial" = true ]; then rm -f -- "$partial"; fi
  if [ "$owned_partial_checksum" = true ]; then rm -f -- "$partial_checksum"; fi
  if [ "$checksum_published" = true ] && [ "$artifact_published" = false ]; then
    rm -f -- "$checksum_path"
  fi
  if [ "${lease_created:-false}" = true ]; then
    if ! "$script_dir/kubernetes-maintenance-lock.sh" release "$kube_context" index-01-hook \
      "$holder_identity"; then
      status=1
    fi
  fi
  return "$status"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

lock_token_dir=$(mktemp -d "${TMPDIR:-/tmp}/index01-backup-lock.XXXXXX") || fail
holder_identity="index-01-hook-backup-$(basename "$lock_token_dir")"
rmdir "$lock_token_dir" || fail
"$script_dir/kubernetes-maintenance-lock.sh" acquire "$kube_context" index-01-hook \
  "$holder_identity" || fail
lease_created=true

replica_status=$(kubectl --context="$kube_context" get deployment/index-01-hook \
  -n "$namespace" -o jsonpath='{.spec.replicas} {.status.readyReplicas}') || fail
[ "$replica_status" = "1 1" ] || fail

umask 077
set -C
if ! { exec 3>"$partial"; } 2>/dev/null; then
  set +C
  fail
fi
set +C
owned_partial=true
chmod 0600 "$partial" || fail

if ! mkfifo -m 0600 "$pipe_path"; then
  fail
fi
owned_pipe=true
kubectl --context="$kube_context" exec -n "$namespace" deployment/index-01-hook -- \
  /index-01-hook backup - >"$pipe_path" 2>/dev/null &
producer_pid=$!
age -r "$age_recipient" -o - <"$pipe_path" >&3 2>/dev/null &
age_pid=$!

encryption_status=0
if wait "$age_pid"; then
  :
else
  encryption_status=$?
fi
age_pid=
if [ "$encryption_status" -ne 0 ]; then
  kill "$producer_pid" 2>/dev/null || true
fi
producer_status=0
if wait "$producer_pid"; then
  :
else
  producer_status=$?
fi
producer_pid=

[ "$producer_status" -eq 0 ] || fail
[ "$encryption_status" -eq 0 ] || fail
rm -f -- "$pipe_path"
owned_pipe=false
exec 3>&-
chmod 0600 "$partial" || fail

checksum_output=$(shasum -a 256 "$partial" 2>/dev/null) || fail
checksum=$(printf '%s\n' "$checksum_output" | awk '
  NF == 2 && length($1) == 64 && $1 !~ /[^0-9a-f]/ { value = $1; valid++ }
  END { if (NR != 1 || valid != 1) exit 1; print value }
') || fail
case "$checksum" in
  [!0-9a-f]*|'') fail ;;
esac

set -C
if ! { exec 4>"$partial_checksum"; } 2>/dev/null; then
  set +C
  fail
fi
set +C
owned_partial_checksum=true
printf '%s  %s\n' "$checksum" "$destination_name" >&4 || fail
exec 4>&-
chmod 0600 "$partial_checksum" || fail

if ! link_no_follow "$partial_checksum" "$checksum_path"; then
  fail
fi
checksum_published=true
rm -f -- "$partial_checksum"
owned_partial_checksum=false

if ! link_no_follow "$partial" "$destination"; then
  fail
fi
artifact_published=true
rm -f -- "$partial"
owned_partial=false

printf 'Backup: %s\nChecksum: %s\n' "$destination" "$checksum_path"
