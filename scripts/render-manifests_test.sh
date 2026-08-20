#!/bin/sh
# shellcheck disable=SC2016
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)
renderer=$project_dir/scripts/render-manifests.sh
source_dir=$project_dir/deploy/kubernetes
test_root=$(mktemp -d "${TMPDIR:-/tmp}/render-manifests-test.XXXXXX")
cleanup() {
  rm -f "$source_dir/index-01-hook-secrets.env" "$source_dir/unexpected.yaml"
  if [ ! -f "$source_dir/ingress.yaml.tmpl" ] && [ -f "$test_root/ingress.yaml.tmpl" ]; then
    mv -f "$test_root/ingress.yaml.tmpl" "$source_dir/ingress.yaml.tmpl"
  fi
  rm -rf "$test_root"
}
trap cleanup EXIT HUP INT TERM

valid_digest=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
valid_ref=registry.example/index-01-hook@sha256:$valid_digest
valid_host=hooks.example.test
valid_class=traefik
valid_tls=index-01-hook-tls
valid_storage=fast-ssd

run_renderer() {
  mode=$1
  image=$2
  output=$3
  host=${4:-$valid_host}
  class=${5:-$valid_class}
  tls=${6:-$valid_tls}
  storage=${7:-}
  REGISTRY_ACCESS_MODE=$mode \
  KUBE_INGRESS_HOST=$host \
  KUBE_INGRESS_CLASS=$class \
  KUBE_TLS_SECRET=$tls \
  KUBE_STORAGE_CLASS=$storage \
    "$renderer" "$image" "$output"
}

expect_reject() {
  case_name=$1
  shift
  output_dir=$test_root/$case_name
  if "$@" >"$test_root/$case_name.stdout" 2>"$test_root/$case_name.stderr"; then
    echo "FAIL: accepted invalid input for $case_name" >&2
    exit 1
  fi
  [ ! -e "$output_dir" ] || {
    echo "FAIL: created output for $case_name" >&2
    exit 1
  }
}

for registry_access_mode in public private; do
  valid_output=$test_root/$registry_access_mode
  run_renderer "$registry_access_mode" "$valid_ref" "$valid_output" "custom.example.test" "custom-class" "custom-tls" "$valid_storage"
  expected='deployment.yaml maintenance-pod.yaml ingress.yaml pvc.yaml namespace.yaml service-account.yaml service.yaml network-policy.yaml'
  for manifest in $expected; do
    [ -f "$valid_output/$manifest" ] || {
      echo "FAIL: missing $registry_access_mode/$manifest" >&2
      exit 1
    }
    if grep -Eiq 'PLACEHOLDER|REPLACE_WITH|CHANGEME|TODO' "$valid_output/$manifest"; then
      echo "FAIL: $registry_access_mode/$manifest contains a placeholder" >&2
      exit 1
    fi
    if grep -F 'kind: Secret' "$valid_output/$manifest" >/dev/null; then
      echo "FAIL: $registry_access_mode/$manifest contains a Secret manifest" >&2
      exit 1
    fi
  done
  [ "$(find "$valid_output" -type f | wc -l | tr -d ' ')" -eq 8 ] || {
    echo "FAIL: $registry_access_mode output has unexpected files" >&2
    exit 1
  }
  for manifest in deployment.yaml maintenance-pod.yaml; do
    grep -F "$valid_ref" "$valid_output/$manifest" >/dev/null || {
      echo "FAIL: $registry_access_mode/$manifest does not contain IMAGE_REF" >&2
      exit 1
    }
    if [ "$registry_access_mode" = public ]; then
      ! grep -F 'imagePullSecrets:' "$valid_output/$manifest" >/dev/null || exit 1
    else
      [ "$(grep -cF 'imagePullSecrets:' "$valid_output/$manifest")" -eq 1 ] || exit 1
      [ "$(grep -cF -- '- name: index-01-hook-registry-pull' "$valid_output/$manifest")" -eq 1 ] || exit 1
    fi
  done
done

ingress=$test_root/public/ingress.yaml
pvc=$test_root/public/pvc.yaml
[ "$(grep -cF 'custom.example.test' "$ingress")" -eq 2 ] || exit 1
grep -F 'ingressClassName: custom-class' "$ingress" >/dev/null || exit 1
grep -F 'secretName: custom-tls' "$ingress" >/dev/null || exit 1
grep -F 'storageClassName: fast-ssd' "$pvc" >/dev/null || exit 1
[ "$(grep -Ec '^          - path: /(webhook|readyz)$' "$ingress")" -eq 2 ] || exit 1
! grep -Eq '^          - path: /(healthz|statusz)$' "$ingress" || exit 1
! grep -Eiq 'cert-manager|external-dns|nginx|cloudflare' "$ingress" || exit 1

# Empty storage class uses the cluster default and emits no storageClassName.
no_storage=$test_root/no-storage
run_renderer public "$valid_ref" "$no_storage"
! grep -F 'storageClassName:' "$no_storage/pvc.yaml" >/dev/null || exit 1

# Replacement swaps the complete output set, so stale files cannot survive.
printf stale >"$test_root/public/stale.yaml"
run_renderer private "$valid_ref" "$test_root/public"
[ ! -e "$test_root/public/stale.yaml" ] || exit 1

# A transform failure leaves an existing output untouched.
failure_bin=$test_root/transform-failure-bin
original_path=$PATH
mkdir -p "$failure_bin"
printf '%s\n' '#!/bin/sh' 'echo "forced awk failure" >&2' 'exit 23' >"$failure_bin/awk"
chmod 0755 "$failure_bin/awk"
mkdir "$test_root/sentinel-output"
printf sentinel >"$test_root/sentinel-output/old.yaml"
if PATH="$failure_bin:$PATH" run_renderer private "$valid_ref" "$test_root/sentinel-output" \
  >"$test_root/transform-failure.stdout" 2>"$test_root/transform-failure.stderr"; then
  echo "FAIL: accepted a transform failure" >&2
  exit 1
fi
grep -Fx 'sentinel' "$test_root/sentinel-output/old.yaml" >/dev/null || exit 1
grep -Fx 'forced awk failure' "$test_root/transform-failure.stderr" >/dev/null || exit 1
PATH=$original_path

# Registry mode and all operator inputs are required.
expect_reject absent-mode env -u REGISTRY_ACCESS_MODE KUBE_INGRESS_HOST=$valid_host KUBE_INGRESS_CLASS=$valid_class KUBE_TLS_SECRET=$valid_tls KUBE_STORAGE_CLASS= "$renderer" "$valid_ref" "$test_root/absent-mode"
expect_reject missing-host env REGISTRY_ACCESS_MODE=public -u KUBE_INGRESS_HOST KUBE_INGRESS_CLASS=$valid_class KUBE_TLS_SECRET=$valid_tls "$renderer" "$valid_ref" "$test_root/missing-host"
expect_reject invalid-mode env REGISTRY_ACCESS_MODE=internal KUBE_INGRESS_HOST=$valid_host KUBE_INGRESS_CLASS=$valid_class KUBE_TLS_SECRET=$valid_tls "$renderer" "$valid_ref" "$test_root/invalid-mode"

for case_name in whitespace shell placeholder malformed; do
  case "$case_name" in
    whitespace) value='bad host' ;;
    shell) value='bad$(id)' ;;
    placeholder) value='REPLACE_WITH_HOST' ;;
    malformed) value='bad..host' ;;
  esac
  expect_reject "host-$case_name" run_renderer public "$valid_ref" "$test_root/host-$case_name" "$value" "$valid_class" "$valid_tls"
done
expect_reject host-single-label run_renderer public "$valid_ref" "$test_root/host-single-label" hook "$valid_class" "$valid_tls"
expect_reject host-ip-address run_renderer public "$valid_ref" "$test_root/host-ip-address" 192.0.2.10 "$valid_class" "$valid_tls"
expect_reject class-shell run_renderer public "$valid_ref" "$test_root/class-shell" "$valid_host" 'class;id' "$valid_tls"
expect_reject tls-whitespace run_renderer public "$valid_ref" "$test_root/tls-whitespace" "$valid_host" "$valid_class" 'tls secret'
expect_reject storage-placeholder run_renderer public "$valid_ref" "$test_root/storage-placeholder" "$valid_host" "$valid_class" "$valid_tls" 'PLACEHOLDER'
unsafe_output="$test_root/unsafe;id"
if run_renderer public "$valid_ref" "$unsafe_output" >/dev/null 2>&1; then
  echo "FAIL: accepted an unsafe output path" >&2
  exit 1
fi
[ ! -e "$unsafe_output" ] || exit 1

# Equivalent source-directory paths must fail without changing any source file.
source_snapshot=$(find "$source_dir" -maxdepth 1 -type f -exec shasum -a 256 {} \; | LC_ALL=C sort)
if run_renderer public "$valid_ref" "$project_dir/deploy//kubernetes" >/dev/null 2>&1; then
  echo "FAIL: accepted an output alias for the Kubernetes source directory" >&2
  exit 1
fi
[ "$(find "$source_dir" -maxdepth 1 -type f -exec shasum -a 256 {} \; | LC_ALL=C sort)" = "$source_snapshot" ] || {
  echo "FAIL: rejected source alias changed Kubernetes sources" >&2
  exit 1
}

for image in \
  "registry.example/index-01-hook@sha256:${valid_digest%?}" \
  "registry.example/index-01-hook@sha256:${valid_digest}0" \
  "registry.example/index-01-hook@sha256:F${valid_digest#?}" \
  "registry.example/index-01-hook:test" \
  "index-01-hook@sha256:$valid_digest" \
  'registry.example/index-01-hook;id@sha256:'"$valid_digest"; do
  expect_reject "image-$(printf '%s' "$image" | cksum | cut -d' ' -f1)" run_renderer public "$image" "$test_root/image-reject"
done

# A real environment file is ignored and is never copied to output.
printf 'INDEX01_WEBHOOK_TOKEN=synthetic-webhook-token-0123456789abcdef\n' >"$source_dir/index-01-hook-secrets.env"
run_renderer public "$valid_ref" "$test_root/real-secret"
[ ! -e "$test_root/real-secret/index-01-hook-secrets.env" ] || exit 1
rm -f "$source_dir/index-01-hook-secrets.env"

# Source inventory checks reject missing and unexpected sources.
mv -f "$source_dir/ingress.yaml.tmpl" "$test_root/ingress.yaml.tmpl"
if run_renderer public "$valid_ref" "$test_root/missing-template" >/dev/null 2>&1; then exit 1; fi
mv -f "$test_root/ingress.yaml.tmpl" "$source_dir/ingress.yaml.tmpl"
printf bad >"$source_dir/unexpected.yaml"
if run_renderer public "$valid_ref" "$test_root/unexpected-source" >/dev/null 2>&1; then exit 1; fi
rm -f "$source_dir/unexpected.yaml"

echo "PASS: portable manifest rendering and validation"
