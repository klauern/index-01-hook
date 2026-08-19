#!/bin/sh
# shellcheck disable=SC2016
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/workload-security-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

assert_contains() {
  file=$1
  value=$2
  grep -F -- "$value" "$file" >/dev/null || {
    echo "FAIL: $file does not contain $value" >&2
    exit 1
  }
}

namespace=$project_dir/deploy/kubernetes/namespace.yaml
service_account=$project_dir/deploy/kubernetes/service-account.yaml
network_policy=$project_dir/deploy/kubernetes/network-policy.yaml
ingress=$project_dir/deploy/kubernetes/ingress.yaml.tmpl
deployment=$project_dir/deploy/kubernetes/deployment.yaml.tmpl
maintenance=$project_dir/deploy/kubernetes/maintenance-pod.yaml.tmpl
secrets_example=$project_dir/deploy/kubernetes/index-01-hook-secrets.env.example

assert_contains "$namespace" "pod-security.kubernetes.io/enforce: restricted"
assert_contains "$namespace" "pod-security.kubernetes.io/enforce-version: v1.31"
assert_contains "$namespace" "pod-security.kubernetes.io/audit-version: v1.31"
assert_contains "$namespace" "pod-security.kubernetes.io/warn-version: v1.31"
assert_contains "$service_account" "automountServiceAccountToken: false"
for template in "$deployment" "$maintenance"; do
  assert_contains "$template" "serviceAccountName: index-01-hook"
  assert_contains "$template" "automountServiceAccountToken: false"
  assert_contains "$template" "privileged: false"
  assert_contains "$template" "allowPrivilegeEscalation: false"
  assert_contains "$template" "readOnlyRootFilesystem: true"
  assert_contains "$template" "type: RuntimeDefault"
  assert_contains "$template" "fsGroupChangePolicy: OnRootMismatch"
done
assert_contains "$maintenance" "requests:"
assert_contains "$maintenance" "limits:"

assert_contains "$network_policy" "name: index-01-hook-default-deny"
assert_contains "$network_policy" "name: index-01-hook-webhook-ingress"
if grep -F 'from:' "$network_policy" >/dev/null; then
  echo "FAIL: portable ingress policy must omit the source peer to allow routed sources" >&2
  exit 1
fi
assert_contains "$network_policy" "port: 8080"
assert_contains "$network_policy" "name: index-01-hook-provider-egress"
assert_contains "$network_policy" "protocol: UDP"
assert_contains "$network_policy" "protocol: TCP"
assert_contains "$network_policy" "port: 53"
assert_contains "$network_policy" "port: 443"
assert_contains "$network_policy" "cidr: 0.0.0.0/0"
assert_contains "$network_policy" "cidr: ::/0"
for private_range in \
  0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 127.0.0.0/8 169.254.0.0/16 \
  172.16.0.0/12 192.0.0.0/24 192.0.2.0/24 192.31.196.0/24 \
  192.52.193.0/24 192.88.99.0/24 192.168.0.0/16 192.175.48.0/24 \
  198.18.0.0/15 198.51.100.0/24 203.0.113.0/24 224.0.0.0/4 240.0.0.0/4 \
  ::/128 ::1/128 64:ff9b:1::/48 100::/64 2001::/23 \
  2001:db8::/32 2002::/16 2620:4f:8000::/48 3fff::/20 5f00::/16 \
  fc00::/7 fe80::/10 ff00::/8; do
  assert_contains "$network_policy" "$private_range"
done
kubernetes_doc=$project_dir/docs/kubernetes.md
assert_contains "$kubernetes_doc" "selected egress control"
assert_contains "$kubernetes_doc" 'cannot restrict TCP `443` by DNS name'
assert_contains "$kubernetes_doc" "provider-only"
assert_contains "$kubernetes_doc" "operator approval before production use"
assert_contains "$kubernetes_doc" "fail-closed egress proxy"
for old_assumption in ingress-nginx kube-system kube-dns; do
  if grep -F "$old_assumption" "$network_policy" >/dev/null; then
    echo "FAIL: network policy contains old assumption $old_assumption" >&2
    exit 1
  fi
done
if grep -F 'annotations:' "$ingress" >/dev/null ||
  grep -Eiq 'cert-manager|external-dns|nginx|cloudflare' "$ingress"; then
  echo "FAIL: portable ingress contains controller assumptions" >&2
  exit 1
fi
if [ "$(grep -Ec '^          - path: /(webhook|readyz)$' "$ingress")" -ne 2 ]; then
  echo "FAIL: ingress must publish exactly /webhook and /readyz" >&2
  exit 1
fi
if grep -Eq '^          - path: /(healthz|statusz)$' "$ingress"; then
  echo "FAIL: ingress publishes a private health route" >&2
  exit 1
fi

for required_key in \
  INDEX01_WEBHOOK_TOKEN \
  INDEX01_DEEPSEEK_TOKEN \
  INDEX01_TICKTICK_TOKEN \
  INDEX01_TICKTICK_DEFAULT_PROJECT_ID \
  INDEX01_TICKTICK_NOTE_PROJECT_ID; do
  grep -Fx "$required_key=" "$secrets_example" >/dev/null || {
    echo "FAIL: secret example must leave $required_key empty" >&2
    exit 1
  }
done
if grep -F 'REPLACE_WITH_' "$secrets_example" >/dev/null; then
  echo "FAIL: secret example contains a usable placeholder" >&2
  exit 1
fi
assert_contains "$secrets_example" "INDEX01_TIME_ZONE=UTC"
assert_contains "$secrets_example" "INDEX01_TICKTICK_PROJECT_ALIASES={}"
if grep -E '^INDEX01_(DB_PATH|LISTEN_ADDR|WORKER_OWNER)=' "$secrets_example" >/dev/null; then
  echo "FAIL: secret example contains workload-owned settings" >&2
  exit 1
fi
assert_contains "$project_dir/.gitignore" "/deploy/kubernetes/index-01-hook-secrets.env"

[ "$(grep -cF '"$artifact_dir/manifests"/*.yaml' "$project_dir/Taskfile.yml")" -eq 2 ] || {
  echo "FAIL: client and server dry-runs do not validate the rendered set" >&2
  exit 1
}
if grep -E 'deploy/kubernetes/(namespace|service-account|network-policy|pvc|service|ingress)\.yaml' "$project_dir/Taskfile.yml" >/dev/null; then
  echo "FAIL: Taskfile applies source Kubernetes manifests" >&2
  exit 1
fi
if grep -F 'maintenance-pod.yaml"' "$project_dir/Taskfile.yml" | grep -F 'apply' >/dev/null; then
  echo "FAIL: deploy applies maintenance Pod" >&2
  exit 1
fi
if grep -F 'certificate' "$project_dir/Taskfile.yml" >/dev/null; then
  echo "FAIL: status queries certificate resources" >&2
  exit 1
fi

fake_bin=$test_root/bin
mkdir -p "$fake_bin"
cat >"$fake_bin/kubectl" <<'EOF'
#!/bin/sh
set -eu
if [ "${1:-}" = config ] && [ "${2:-}" = current-context ]; then
  printf '%s\n' test-context
  exit 0
fi
case " $* " in
  *" create -f - "*)
    manifest=$(cat)
    holder=$(printf '%s\n' "$manifest" | sed -n 's/.*"holderIdentity":"\([A-Za-z0-9._-]*\)".*/\1/p')
    printf '%s\n' "$holder" >"$FAKE_LEASE_STATE"
    ;;
  *" patch lease/index-01-hook-maintenance-lock "*)
    printf '%s\n' released >"$FAKE_LEASE_STATE"
    ;;
  *" get deployment/index-01-hook "*) printf '1 1' ;;
  *" exec -n index-01-hook "*)
    if [ "${FAKE_KUBECTL_FAIL:-}" = 1 ]; then
      printf 'partial private backup'
      exit 23
    fi
    printf 'SQLite format 3\000private backup content'
    ;;
  *) exit 91 ;;
esac
EOF
chmod 0700 "$fake_bin/kubectl"

cat >"$fake_bin/age" <<'EOF'
#!/bin/sh
set -eu
if [ "${FAKE_AGE_FAIL:-}" = 1 ]; then
  exit 24
fi
checksum=$(shasum -a 256 | awk '{print $1}')
printf 'age-encrypted-test:%s\n' "$checksum"
EOF
chmod 0700 "$fake_bin/age"

destination=$test_root/backup.db.age
PATH="$fake_bin:$PATH" FAKE_LEASE_STATE="$test_root/lease" task --yes --dir "$project_dir" backup-export \
  DESTINATION="$destination" \
  AGE_RECIPIENT=age1testrecipient \
  KUBE_CONTEXT=test-context >/dev/null

[ -f "$destination" ] || {
  echo "FAIL: encrypted backup was not published" >&2
  exit 1
}
[ -f "$destination.sha256" ] || {
  echo "FAIL: encrypted backup checksum was not published" >&2
  exit 1
}
if grep -aF "private backup content" "$destination" >/dev/null; then
  echo "FAIL: backup export published plaintext" >&2
  exit 1
fi
(cd "$test_root" && shasum -a 256 -c "$(basename "$destination.sha256")" >/dev/null)

failed_destination=$test_root/failed.db.age
if PATH="$fake_bin:$PATH" FAKE_LEASE_STATE="$test_root/lease" FAKE_KUBECTL_FAIL=1 task --yes --dir "$project_dir" backup-export \
  DESTINATION="$failed_destination" \
  AGE_RECIPIENT=age1testrecipient \
  KUBE_CONTEXT=test-context >/dev/null 2>&1; then
  echo "FAIL: backup export accepted a failed database stream" >&2
  exit 1
fi
if find "$test_root" -maxdepth 1 -name 'failed.db.age*' -print | grep . >/dev/null; then
  echo "FAIL: failed backup export left an artifact" >&2
  exit 1
fi

encryption_failure=$test_root/encryption-failed.db.age
if PATH="$fake_bin:$PATH" FAKE_LEASE_STATE="$test_root/lease" FAKE_AGE_FAIL=1 task --yes --dir "$project_dir" backup-export \
  DESTINATION="$encryption_failure" \
  AGE_RECIPIENT=age1testrecipient \
  KUBE_CONTEXT=test-context >/dev/null 2>&1; then
  echo "FAIL: backup export accepted failed encryption" >&2
  exit 1
fi
if find "$test_root" -maxdepth 1 -name 'encryption-failed.db.age*' -print | grep . >/dev/null; then
  echo "FAIL: failed encryption left an artifact" >&2
  exit 1
fi

echo "PASS: workload and encrypted backup boundaries"
