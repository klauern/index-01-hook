#!/bin/sh
set -eu
script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/index01-offline-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
mkdir "$test_root/bin"
cat >"$test_root/bin/go" <<'STUB'
#!/bin/sh
set -eu
[ "${INDEX01_RUN_LIVE_DEEPSEEK_TEST:-}" = 0 ]
[ "${INDEX01_RUN_LIVE_EVALUATION:-}" = 0 ]
[ "${INDEX01_RUN_LIVE_CORPUS_EVALUATION:-}" = 0 ]
[ "${INDEX01_EVAL_CORPUS:-}" = '' ]
[ "${INDEX01_EVAL_CORPUS_MODE:-}" = replay ]
[ "${INDEX01_EVAL_REPORT_DIR:-}" = dist/evaluation ]
[ "${INDEX01_DEEPSEEK_TOKEN+x}" = '' ]
[ "${INDEX01_TICKTICK_TOKEN+x}" = '' ]
[ "${INDEX01_TYPESAFE_TOKEN+x}" = '' ]
[ "${INDEX01_TYPESAFE_SPIKE_APPROVED+x}" = '' ]
[ "${INDEX01_TYPESAFE_CALIBRATION_APPROVED+x}" = '' ]
printf '%s %s\n' "$(basename "$0")" "$*" >>"$CHECK_LOG"
if [ "${SIGNAL_GATE:-}" = "$*" ]; then kill -"$CANCEL_SIGNAL" "$PPID"; fi
if [ "${FAIL_GATE:-}" = "$*" ]; then exit 9; fi
STUB
cp -f "$test_root/bin/go" "$test_root/bin/task"
chmod +x "$test_root/bin/go" "$test_root/bin/task"
export PATH="$test_root/bin:$PATH" CHECK_LOG="$test_root/checks"
export INDEX01_RUN_LIVE_DEEPSEEK_TEST=1 INDEX01_RUN_LIVE_EVALUATION=1
export INDEX01_RUN_LIVE_CORPUS_EVALUATION=1 INDEX01_EVAL_CORPUS=private-synthetic-path
export INDEX01_EVAL_CORPUS_MODE=live INDEX01_EVAL_REPORT_DIR=external-synthetic-reports
export INDEX01_DEEPSEEK_TOKEN=synthetic INDEX01_TICKTICK_TOKEN=synthetic INDEX01_TYPESAFE_TOKEN=synthetic
export INDEX01_TYPESAFE_SPIKE_APPROVED=true INDEX01_TYPESAFE_CALIBRATION_APPROVED=true
"$script_dir/validate-offline.sh" >"$test_root/output" 2>&1
[ "$(wc -l <"$CHECK_LOG" | tr -d ' ')" = 6 ]
grep -Fxq 'go test -count=1 ./...' "$CHECK_LOG"
grep -Fxq 'go test -race -count=1 ./...' "$CHECK_LOG"
grep -Fxq 'task test' "$CHECK_LOG"
grep -Fxq 'task eval' "$CHECK_LOG"
: >"$CHECK_LOG"
export FAIL_GATE='test -count=1 ./...'
if "$script_dir/validate-offline.sh" >"$test_root/output" 2>&1; then
  echo 'FAIL: validation accepted a failed gate' >&2
  exit 1
fi
[ "$(wc -l <"$CHECK_LOG" | tr -d ' ')" = 6 ]
grep -Fq 'FAIL: Go tests (exit 9)' "$test_root/output"
grep -Fq 'PASS: Build' "$test_root/output"
unset FAIL_GATE
export SIGNAL_GATE='test -count=1 ./...'
for signal_case in HUP:129 INT:130 TERM:143; do
  CANCEL_SIGNAL=${signal_case%:*}
  expected_status=${signal_case#*:}
  export CANCEL_SIGNAL
  : >"$CHECK_LOG"
  status=0
  "$script_dir/validate-offline.sh" >"$test_root/output" 2>&1 || status=$?
  if [ "$status" -ne "$expected_status" ]; then
    echo "FAIL: $CANCEL_SIGNAL returned $status, expected $expected_status" >&2
    exit 1
  fi
  [ "$(wc -l <"$CHECK_LOG" | tr -d ' ')" = 1 ]
  if grep -Fq 'PASS: Build' "$test_root/output"; then
    echo 'FAIL: validation continued after cancellation' >&2
    exit 1
  fi
done
echo 'PASS: offline validation isolates settings, reports failures, and stops after cancellation'
