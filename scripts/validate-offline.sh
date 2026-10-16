#!/bin/sh
set -eu

# Disable inherited provider approval and private corpus selection.
unset INDEX01_DEEPSEEK_TOKEN INDEX01_TICKTICK_TOKEN INDEX01_TYPESAFE_TOKEN
unset INDEX01_TYPESAFE_SPIKE_APPROVED INDEX01_TYPESAFE_CALIBRATION_APPROVED
export INDEX01_RUN_LIVE_DEEPSEEK_TEST=0 INDEX01_RUN_LIVE_EVALUATION=0
export INDEX01_RUN_LIVE_CORPUS_EVALUATION=0 INDEX01_EVAL_CORPUS=
export INDEX01_EVAL_CORPUS_MODE=replay

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
cd "$script_dir/.."
artifact_dir=$(mktemp -d "${TMPDIR:-/tmp}/index01-offline.XXXXXX")
trap 'rm -rf "$artifact_dir"' EXIT HUP INT TERM

# Continue after a failure so the report identifies every failed gate.
result=0
check() {
  label=$1
  shift
  if "$@"; then
    printf 'PASS: %s\n' "$label"
  else
    status=$?
    printf 'FAIL: %s (exit %s)\n' "$label" "$status" >&2
    result=1
  fi
}
check 'Go tests' go test -count=1 ./...
check 'Go race tests' go test -race -count=1 ./...
check 'Go vet' go vet ./...
check 'Task checks' task test
check 'Offline evaluation' task eval
check 'Build' go build -o "$artifact_dir/index-01-hook" .
exit "$result"
