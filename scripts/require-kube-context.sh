#!/bin/sh
set -eu

expected_context=${1:-}
case "$expected_context" in
  ''|*[!A-Za-z0-9._:@/-]*)
    echo "KUBE_CONTEXT contains an invalid character" >&2
    exit 1
    ;;
esac

current_context=$(kubectl config current-context)
if [ "$current_context" != "$expected_context" ]; then
  echo "KUBE_CONTEXT does not match the current Kubernetes context" >&2
  exit 1
fi
