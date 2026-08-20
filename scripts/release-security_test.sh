#!/bin/sh
# shellcheck disable=SC2016
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
validator=$script_dir/validate-release-inputs.sh
context_validator=$script_dir/require-kube-context.sh
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)
task_path=$(command -v task)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/release-security-test.XXXXXX")
fake_bin=$test_root/bin
mkdir -p "$fake_bin"
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

expect_reject() {
  case_name=$1
  shift
  if "$validator" "$@" >"$test_root/$case_name.stdout" 2>"$test_root/$case_name.stderr"; then
    fail "accepted invalid release input for $case_name"
  fi
}

digest=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
builder=registry.example/golang/build@sha256:$digest
runtime=ghcr.io/example-owner/index-01-hook@sha256:$digest

"$validator" metadata v0.1.0 0123456789abcdef 2026-08-13T12:00:00Z
"$validator" immutable-image "$builder" GO_IMAGE
"$validator" immutable-image "$runtime" IMAGE_REF
"$validator" output-image ghcr.io/example-owner/index-01-hook:v0.1.0

expect_reject version-semicolon metadata 'v0.1.0;id' 0123456 2026-08-13T12:00:00Z
expect_reject version-substitution metadata 'v0.1.0$(id)' 0123456 2026-08-13T12:00:00Z
expect_reject version-backtick metadata 'v0.1.0`id`' 0123456 2026-08-13T12:00:00Z
expect_reject version-space metadata 'v0.1.0 bad' 0123456 2026-08-13T12:00:00Z
expect_reject version-newline metadata 'v0.1.0
bad' 0123456 2026-08-13T12:00:00Z
expect_reject build-date-yaml metadata v0.1.0 0123456 '2026-08-13T12:00:00Z
image: bad'
expect_reject builder-tag immutable-image registry.example/golang/build:latest GO_IMAGE
expect_reject builder-short-name immutable-image "golang@sha256:$digest" GO_IMAGE
expect_reject runtime-tag immutable-image ghcr.io/example-owner/index-01-hook:latest IMAGE_REF
expect_reject runtime-yaml immutable-image "ghcr.io/example-owner/index-01-hook@sha256:$digest
image: bad" IMAGE_REF
expect_reject output-tag-space output-image 'ghcr.io/example-owner/index-01-hook:bad tag'

if "$task_path" --silent --dir "$project_dir" build \
  VERSION='v0.1.0; exit 0' \
  COMMIT=0123456 \
  BUILD_DATE=2026-08-13T12:00:00Z \
  ARTIFACT_DIR="$test_root/injection-artifacts" \
  >"$test_root/task-injection.stdout" 2>"$test_root/task-injection.stderr"; then
  fail "Task build accepted a command separator in VERSION"
fi
[ ! -e "$test_root/injection-artifacts" ] ||
  fail "Task build created output before it rejected VERSION"

cat >"$fake_bin/kubectl" <<'FAKE_KUBECTL'
#!/bin/sh
set -eu
if [ "$#" -eq 2 ] && [ "$1" = config ] && [ "$2" = current-context ]; then
  printf '%s\n' test-context
  exit 0
fi

[ "${1:-}" = --context=test-context ] || exit 90
shift
case "${1:-}" in
  apply)
    call=$*
    case "$call" in
      *--dry-run=client*--validate=strict*) ;;
      *) exit 91 ;;
    esac
    printf 'apply\n' >>"$FAKE_KUBECTL_LOG"
    ;;
  create)
    call=$*
    case "$call" in
      *--dry-run=client*--validate=strict*) ;;
      *) exit 92 ;;
    esac
    printf 'create\n' >>"$FAKE_KUBECTL_LOG"
    printf '%s' "$FAKE_KUBECTL_IMAGE"
    ;;
  *) exit 93 ;;
esac
FAKE_KUBECTL
chmod 0755 "$fake_bin/kubectl"

PATH="$fake_bin" "$context_validator" test-context
if PATH="$fake_bin" "$context_validator" another-context >"$test_root/context.stdout" 2>"$test_root/context.stderr"; then
  fail "accepted a Kubernetes context mismatch"
fi
if PATH="$fake_bin" "$context_validator" 'test-context;id' >"$test_root/context-invalid.stdout" 2>"$test_root/context-invalid.stderr"; then
  fail "accepted an invalid Kubernetes context"
fi

kubectl_log=$test_root/kubectl.log
: >"$kubectl_log"
PATH="$fake_bin:$PATH" \
  FAKE_KUBECTL_LOG="$kubectl_log" \
  FAKE_KUBECTL_IMAGE="$runtime" \
  "$task_path" --silent --dir "$project_dir" dry-run \
  IMAGE_REF="$runtime" \
  REGISTRY_ACCESS_MODE=private \
  KUBE_CONTEXT=test-context \
  KUBE_INGRESS_HOST=hooks.example.test \
  KUBE_INGRESS_CLASS=traefik \
  KUBE_TLS_SECRET=index-01-hook-tls \
  ARTIFACT_DIR="$test_root/artifacts"

[ "$(grep -c '^apply$' "$kubectl_log")" -eq 8 ] ||
  fail "strict client validation did not process eight manifests"
[ "$(grep -c '^create$' "$kubectl_log")" -eq 2 ] ||
  fail "image parsing did not process two workload manifests"

if PATH="$fake_bin:$PATH" \
  FAKE_KUBECTL_LOG="$kubectl_log" \
  FAKE_KUBECTL_IMAGE="ghcr.io/example-owner/other@sha256:$digest" \
  "$task_path" --silent --dir "$project_dir" dry-run \
  IMAGE_REF="$runtime" \
  REGISTRY_ACCESS_MODE=private \
  KUBE_CONTEXT=test-context \
  KUBE_INGRESS_HOST=hooks.example.test \
  KUBE_INGRESS_CLASS=traefik \
  KUBE_TLS_SECRET=index-01-hook-tls \
  ARTIFACT_DIR="$test_root/mismatch-artifacts" \
  >"$test_root/dry-run-mismatch.stdout" 2>"$test_root/dry-run-mismatch.stderr"; then
  fail "accepted a parsed workload image that differs from IMAGE_REF"
fi

echo "PASS: release input and Kubernetes context validation"
