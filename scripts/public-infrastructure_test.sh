#!/bin/sh
# shellcheck disable=SC2016
set -eu

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd -P)
for required_command in docker kind kubectl kubeconform go mktemp realpath od; do
  command -v "$required_command" >/dev/null 2>&1 || fail "$required_command is required"
done

version_output=$(go version 2>/dev/null) || fail "Go 1.26.6 is required"
case "$version_output" in
  'go version go1.26.6 '*) ;;
  *) fail "Go 1.26.6 is required" ;;
esac
docker info >/dev/null 2>&1 || fail "Docker daemon is unavailable"
docker_context=$(docker context show 2>/dev/null) || fail "Docker context is unavailable"
docker_endpoint=$(docker context inspect "$docker_context" \
  --format '{{(index .Endpoints "docker").Host}}' 2>/dev/null) ||
  fail "Docker endpoint is unavailable"
case "$docker_endpoint" in
  unix:///var/run/docker.sock|unix://"$HOME"/*|unix:///Users/*|unix:///home/*) ;;
  *) fail "Docker endpoint is not local" ;;
esac

test_root=$(mktemp -d "${TMPDIR:-/tmp}/index01-public-infrastructure.XXXXXX")
chmod 0700 "$test_root"
suffix=$(od -An -N6 -tx1 /dev/urandom | tr -d '[:space:]')
kubeconfig=$test_root/kubeconfig
cluster_name=index01-public-$suffix
application_image=index-01-hook-infrastructure:$suffix
cluster_created=0
application_image_created=0
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "${cluster_created:-0}" = 1 ] && [ -n "${cluster_name:-}" ]; then
    KUBECONFIG="$kubeconfig" kind delete cluster --name "$cluster_name" \
      >"$test_root/kind-delete.log" 2>&1 || status=1
    if docker ps -a --filter "label=io.x-k8s.kind.cluster=$cluster_name" --format '{{.ID}}' |
      grep . >/dev/null 2>&1; then
      status=1
    fi
  fi
  if [ "${application_image_created:-0}" = 1 ]; then
    docker image rm -f "$application_image" >/dev/null 2>&1 || status=1
    docker image inspect "$application_image" >/dev/null 2>&1 && status=1
  fi
  if [ "${PUBLIC_INFRA_KEEP_TEMP:-0}" = 1 ]; then
    printf 'Infrastructure diagnostics retained at %s\n' "$test_root" >&2
  else
    rm -rf "$test_root" || status=1
    [ ! -e "$test_root" ] || status=1
  fi
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

run_step() {
  name=$1
  shift
  if ! "$@" >"$test_root/$name.log" 2>&1; then
    fail "$name failed"
  fi
}

caddy_image=docker.io/library/caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d
kind_image=docker.io/kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5
caddyfile=$project_dir/deploy/compose/Caddyfile.example
[ -f "$caddyfile" ] || fail "Caddy example is missing"

run_step caddy-pull docker pull "$caddy_image"
run_step caddy-validate docker run --rm --network none --read-only --cap-drop NET_RAW \
  --security-opt no-new-privileges:true \
  --tmpfs /data:rw,noexec,nosuid,nodev --tmpfs /config:rw,noexec,nosuid,nodev \
  --mount "type=bind,src=$caddyfile,dst=/etc/caddy/Caddyfile,readonly" \
  "$caddy_image" caddy validate --config /etc/caddy/Caddyfile

if docker image inspect "$application_image" >"$test_root/image-collision.log" 2>&1; then
  fail "generated application image already exists"
fi
if ! docker build --tag "$application_image" "$project_dir" >"$test_root/image-build.log" 2>&1; then
  docker image inspect "$application_image" >/dev/null 2>&1 && application_image_created=1
  fail "application image build failed"
fi
application_image_created=1
run_step image-inspect docker image inspect "$application_image"

image_digest=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
image_ref=docker.io/library/index-01-hook@sha256:$image_digest
rendered_dir=$test_root/rendered
mkdir "$rendered_dir"
run_step render env \
  REGISTRY_ACCESS_MODE=public \
  KUBE_INGRESS_HOST=hook.example.test \
  KUBE_INGRESS_CLASS=standard \
  KUBE_TLS_SECRET=index-01-hook-tls \
  KUBE_STORAGE_CLASS=standard \
  "$script_dir/render-manifests.sh" "$image_ref" "$rendered_dir"

for manifest in namespace.yaml service-account.yaml service.yaml network-policy.yaml \
  deployment.yaml maintenance-pod.yaml ingress.yaml pvc.yaml; do
  [ -f "$rendered_dir/$manifest" ] || fail "rendered manifest is missing"
  if grep -Eiq 'PLACEHOLDER|REPLACE_WITH|CHANGEME|TODO|kind: Secret' \
    "$rendered_dir/$manifest" >"$test_root/manifest-check.log" 2>&1; then
    fail "rendered manifest contains private or placeholder data"
  fi
done
run_step kubeconform kubeconform -strict -kubernetes-version 1.31.0 "$rendered_dir"/*.yaml

# An isolated kubeconfig prevents access to any existing context.
if docker ps -a --filter "label=io.x-k8s.kind.cluster=$cluster_name" --format '{{.ID}}' |
  grep . >"$test_root/kind-collision.log" 2>&1; then
  fail "generated kind cluster name already exists"
fi
cluster_created=1
run_step kind-create env KUBECONFIG="$kubeconfig" kind create cluster \
  --name "$cluster_name" --image "$kind_image" --kubeconfig "$kubeconfig" --wait 120s
run_step kind-load env KUBECONFIG="$kubeconfig" kind load docker-image "$application_image" \
  --name "$cluster_name"

kube() {
  kubectl --kubeconfig "$kubeconfig" "$@"
}

# Validate the Namespace first, then create it so the API can validate
# namespaced resources without persisting those resources.
run_step server-dry-run-namespace.yaml kube apply --dry-run=server --validate=strict \
  -f "$rendered_dir/namespace.yaml"
run_step namespace-bootstrap kube apply --validate=strict -f "$rendered_dir/namespace.yaml"
run_step server-dry-run-service-account.yaml kube apply --dry-run=server --validate=strict \
  -f "$rendered_dir/service-account.yaml"
run_step service-account-bootstrap kube apply --validate=strict \
  -f "$rendered_dir/service-account.yaml"
for manifest in service.yaml network-policy.yaml deployment.yaml maintenance-pod.yaml \
  ingress.yaml pvc.yaml; do
  run_step "server-dry-run-$manifest" kube apply --dry-run=server --validate=strict \
    -f "$rendered_dir/$manifest"
done

run_step apply-allowed-resources sh -c '
  set -eu
  kubeconfig=$1
  rendered_dir=$2
  kubectl --kubeconfig "$kubeconfig" apply --validate=strict -f "$rendered_dir/network-policy.yaml"
  kubectl --kubeconfig "$kubeconfig" apply --validate=strict -f "$rendered_dir/pvc.yaml"
' sh "$kubeconfig" "$rendered_dir"
probe_name=index-01-hook-probe
probe_manifest=$test_root/probe.yaml
cat >"$probe_manifest" <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: $probe_name
  namespace: index-01-hook
  labels:
    app.kubernetes.io/name: index-01-hook-probe
spec:
  serviceAccountName: index-01-hook
  automountServiceAccountToken: false
  enableServiceLinks: false
  restartPolicy: Never
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    runAsGroup: 65532
    fsGroup: 65532
    fsGroupChangePolicy: OnRootMismatch
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: probe
      image: $application_image
      imagePullPolicy: Never
      command: ["/index-01-hook", "purge-expired"]
      env:
        - name: INDEX01_DB_PATH
          value: /var/lib/index-01-hook/data/index01.db
        - name: INDEX01_PURGE_CONFIRM
          value: purge-expired-recordings
      securityContext:
        privileged: false
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        capabilities:
          drop: [ALL]
      volumeMounts:
        - name: data
          mountPath: /var/lib/index-01-hook
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: index-01-hook-data
EOF
run_step probe-apply kube apply --validate=strict -f "$probe_manifest"
run_step pvc-bound kube wait --for=jsonpath='{.status.phase}'=Bound \
  pvc/index-01-hook-data -n index-01-hook --timeout=180s
run_step probe-complete kube wait --for=jsonpath='{.status.phase}'=Succeeded \
  "pod/$probe_name" -n index-01-hook --timeout=180s
probe_output=$test_root/probe-output.json
run_step probe-logs kube logs "$probe_name" -n index-01-hook
cp -f "$test_root/probe-logs.log" "$probe_output"
[ "$(cat "$probe_output")" = '{"state":"purged","recordings_deleted":0,"retention_days":30}' ] ||
  fail "probe returned unexpected purge JSON"
run_step probe-delete kube delete pod "$probe_name" -n index-01-hook --wait=true

# Add the exact purge confirmation to a private maintenance copy.
maintenance_manifest=$test_root/maintenance-pod.yaml
awk '
  { print }
  /value: \/var\/lib\/index-01-hook\/data\/index01.db$/ {
    print "        - name: INDEX01_PURGE_CONFIRM"
    print "          value: purge-expired-recordings"
  }
' "$rendered_dir/maintenance-pod.yaml" |
  sed -e "s|image: .*|image: $application_image|" \
      -e 's/imagePullPolicy: IfNotPresent/imagePullPolicy: Never/' >"$maintenance_manifest"
run_step maintenance-apply kube apply --validate=strict -f "$maintenance_manifest"
run_step maintenance-ready kube wait --for=condition=Ready \
  pod/index-01-hook-maintenance -n index-01-hook --timeout=180s

backup_path=$test_root/maintenance-backup.db
run_step maintenance-backup kube exec -n index-01-hook pod/index-01-hook-maintenance -- \
  /index-01-hook backup -
cp -f "$test_root/maintenance-backup.log" "$backup_path"
[ -s "$backup_path" ] || fail "maintenance backup is empty"
backup_magic=$(od -An -tx1 -N16 "$backup_path" | tr -d '[:space:]')
[ "$backup_magic" = 53514c69746520666f726d6174203300 ] || fail "maintenance backup is not SQLite"

run_step maintenance-purge kube exec -n index-01-hook pod/index-01-hook-maintenance -- \
  /index-01-hook purge-expired
[ "$(cat "$test_root/maintenance-purge.log")" = '{"state":"purged","recordings_deleted":0,"retention_days":30}' ] ||
  fail "maintenance returned unexpected purge JSON"

assert_jsonpath() {
  name=$1
  expression=$2
  expected=$3
  actual=$(kube get pod/index-01-hook-maintenance -n index-01-hook -o "jsonpath=$expression" 2>"$test_root/jsonpath-$name.log") ||
    fail "could not inspect maintenance Pod security"
  [ "$actual" = "$expected" ] || fail "maintenance Pod security field differs"
}
assert_jsonpath image '{.spec.containers[0].image}' "$application_image"
assert_jsonpath image-policy '{.spec.containers[0].imagePullPolicy}' Never
assert_jsonpath run-as-non-root '{.spec.securityContext.runAsNonRoot}' true
assert_jsonpath run-as-user '{.spec.securityContext.runAsUser}' 65532
assert_jsonpath run-as-group '{.spec.securityContext.runAsGroup}' 65532
assert_jsonpath fs-group '{.spec.securityContext.fsGroup}' 65532
assert_jsonpath seccomp '{.spec.securityContext.seccompProfile.type}' RuntimeDefault
assert_jsonpath privileged '{.spec.containers[0].securityContext.privileged}' false
assert_jsonpath allow-escalation '{.spec.containers[0].securityContext.allowPrivilegeEscalation}' false
assert_jsonpath read-only-root '{.spec.containers[0].securityContext.readOnlyRootFilesystem}' true
assert_jsonpath capability-drop '{.spec.containers[0].securityContext.capabilities.drop[0]}' ALL

run_step maintenance-delete kube delete pod/index-01-hook-maintenance -n index-01-hook --wait=true
printf 'PASS: public Caddy and disposable Kubernetes validation\n'
