#!/bin/sh
# shellcheck disable=SC2016,SC2086
set -eu

fail() {
  echo "$1" >&2
  exit 1
}

image_ref=${1:-}
output_dir=${2:-}
registry_access_mode=${REGISTRY_ACCESS_MODE:-}
ingress_host=${KUBE_INGRESS_HOST:-}
ingress_class=${KUBE_INGRESS_CLASS:-}
tls_secret=${KUBE_TLS_SECRET:-}
storage_class=${KUBE_STORAGE_CLASS:-}
script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd)
source_dir=$project_dir/deploy/kubernetes

case "$registry_access_mode" in
  public|private) ;;
  *) fail "REGISTRY_ACCESS_MODE must be public or private" ;;
esac

"$script_dir/validate-release-inputs.sh" immutable-image "$image_ref" IMAGE_REF

validate_dns_value() {
  value=$1
  label=$2
  case "$value" in
    ''|*[!a-z0-9.-]*) fail "$label must be a Kubernetes DNS value" ;;
    .*|*.|*..*) fail "$label must be a Kubernetes DNS value" ;;
  esac
  case "$value" in
    *placeholder*|*PLACEHOLDER*|*replace*|*REPLACE*|*changeme*|*CHANGEME*|*todo*|*TODO*|*example.invalid*)
      fail "$label must not contain a placeholder" ;;
  esac
  [ "${#value}" -le 253 ] || fail "$label must be a Kubernetes DNS value"
  old_ifs=$IFS
  IFS=.
  set -- $value
  IFS=$old_ifs
  [ "$#" -gt 0 ] || fail "$label must be a Kubernetes DNS value"
  for label_part in "$@"; do
    [ -n "$label_part" ] || fail "$label must be a Kubernetes DNS value"
    [ "${#label_part}" -le 63 ] || fail "$label must be a Kubernetes DNS value"
    case "$label_part" in
      -*|*-|*[!a-z0-9-]*) fail "$label must be a Kubernetes DNS value" ;;
    esac
  done
}

validate_dns_value "$ingress_host" KUBE_INGRESS_HOST
case "$ingress_host" in
  *.*) ;;
  *) fail "KUBE_INGRESS_HOST must be a fully qualified DNS name" ;;
esac
case "$ingress_host" in
  *[a-z]*) ;;
  *) fail "KUBE_INGRESS_HOST must not be an IP address" ;;
esac
validate_dns_value "$ingress_class" KUBE_INGRESS_CLASS
validate_dns_value "$tls_secret" KUBE_TLS_SECRET
if [ -n "$storage_class" ]; then
  validate_dns_value "$storage_class" KUBE_STORAGE_CLASS
fi

[ -n "$output_dir" ] || fail "output directory is required"
case "$output_dir" in
  *[[:space:]]*|*';'*|*'`'*|*'$'*|*'('*|*')'*|*'&'*|*'|'*|*'<'*|*'>'*|*'*'*|*'?'*|*'['*|*']'*|*'{'*|*'}'*|*\\*)
    fail "output directory contains unsafe characters"
    ;;
  ../*|*/../*|*/..|..)
    fail "output directory contains an unsafe parent path"
    ;;
esac
if [ -L "$output_dir" ]; then
  fail "output directory must not be a symbolic link"
fi
output_parent=$(dirname "$output_dir")
output_name=$(basename "$output_dir")
case "$output_name" in ''|.|..|/) fail "output directory name is invalid" ;; esac
[ ! -L "$output_parent" ] || fail "output parent must not be a symbolic link"
mkdir -p "$output_parent"
canonical_parent=$(CDPATH='' cd "$output_parent" && pwd -P) ||
  fail "output parent cannot be resolved"
canonical_output=$canonical_parent/$output_name
[ "$canonical_output" != "$source_dir" ] || fail "output directory must not be the source directory"
[ "$canonical_output" != "$project_dir" ] || fail "output directory must not be the project directory"
case "$canonical_output" in
  "$source_dir"/*) fail "output directory must not be inside the source directory" ;;
  "$project_dir"/*)
    case "$canonical_output" in
      "$project_dir/dist"|"$project_dir/dist"/*) ;;
      *) fail "output directory inside the project must be under dist" ;;
    esac
    ;;
esac
output_dir=$canonical_output
output_parent=$canonical_parent
if [ -e "$output_dir" ] && [ ! -d "$output_dir" ]; then
  fail "output directory is not a directory"
fi

# Check the source set before creating any output. The example environment file
# is intentionally allowed as source data but is never copied to the output.
expected_sources='namespace.yaml service-account.yaml service.yaml network-policy.yaml deployment.yaml.tmpl maintenance-pod.yaml.tmpl ingress.yaml.tmpl pvc.yaml.tmpl index-01-hook-secrets.env.example'
ignored_sources='index-01-hook-secrets.env'
for expected in $expected_sources; do
  [ -f "$source_dir/$expected" ] || fail "missing Kubernetes source file: $expected"
done
for source in "$source_dir"/* "$source_dir"/.[!.]* "$source_dir"/..?*; do
  [ -e "$source" ] || continue
  [ -f "$source" ] || fail "unexpected Kubernetes source entry: $(basename "$source")"
  source_name=$(basename "$source")
  case " $expected_sources $ignored_sources " in
    *" $source_name "*) ;;
    *) fail "unexpected Kubernetes source file: $source_name" ;;
  esac
done

staging_dir=$(mktemp -d "$output_parent/.render-manifests.XXXXXX")
backup_dir=
cleanup() {
  rm -rf "$staging_dir"
  if [ -n "$backup_dir" ] && [ -e "$backup_dir" ]; then
    if [ -e "$output_dir" ]; then
      rm -rf "$backup_dir"
    else
      mv -f "$backup_dir" "$output_dir" || true
    fi
  fi
}
trap cleanup EXIT HUP INT TERM

cp "$source_dir/namespace.yaml" "$staging_dir/namespace.yaml"
cp "$source_dir/service-account.yaml" "$staging_dir/service-account.yaml"
cp "$source_dir/service.yaml" "$staging_dir/service.yaml"
cp "$source_dir/network-policy.yaml" "$staging_dir/network-policy.yaml"

render_template() {
  template=$1
  output_name=$2
  RENDER_IMAGE_REF=$image_ref \
  RENDER_INGRESS_HOST=$ingress_host \
  RENDER_INGRESS_CLASS=$ingress_class \
  RENDER_TLS_SECRET=$tls_secret \
  RENDER_STORAGE_CLASS=$storage_class \
  awk -v registry_access_mode="$registry_access_mode" '
    function replace_all(line, placeholder, replacement, position) {
      while ((position = index(line, placeholder)) != 0) {
        line = substr(line, 1, position - 1) replacement \
          substr(line, position + length(placeholder))
      }
      return line
    }
    /IMAGE_PULL_SECRETS_PLACEHOLDER/ {
      if (registry_access_mode == "private") {
        match($0, /^ */)
        indent = substr($0, 1, RLENGTH)
        print indent "imagePullSecrets:"
        print indent "  - name: index-01-hook-registry-pull"
      }
      next
    }
    /KUBE_STORAGE_CLASS_PLACEHOLDER/ {
      if (ENVIRON["RENDER_STORAGE_CLASS"] != "")
        print "  storageClassName: " ENVIRON["RENDER_STORAGE_CLASS"]
      next
    }
    {
      line = replace_all($0, "IMAGE_REF_PLACEHOLDER", ENVIRON["RENDER_IMAGE_REF"])
      line = replace_all(line, "KUBE_INGRESS_HOST_PLACEHOLDER", ENVIRON["RENDER_INGRESS_HOST"])
      line = replace_all(line, "KUBE_INGRESS_CLASS_PLACEHOLDER", ENVIRON["RENDER_INGRESS_CLASS"])
      line = replace_all(line, "KUBE_TLS_SECRET_PLACEHOLDER", ENVIRON["RENDER_TLS_SECRET"])
      print replace_all(line, "KUBE_STORAGE_CLASS_PLACEHOLDER", ENVIRON["RENDER_STORAGE_CLASS"])
    }
  ' "$source_dir/$template" >"$staging_dir/$output_name"
}

render_template deployment.yaml.tmpl deployment.yaml
render_template maintenance-pod.yaml.tmpl maintenance-pod.yaml
render_template ingress.yaml.tmpl ingress.yaml
render_template pvc.yaml.tmpl pvc.yaml

for manifest in "$staging_dir"/*.yaml; do
  [ -f "$manifest" ] || fail "rendered manifest is not a regular file"
  if grep -Eiq 'PLACEHOLDER|REPLACE_WITH|CHANGEME|TODO' "$manifest"; then
    fail "rendered manifest contains a placeholder: $(basename "$manifest")"
  fi
  if grep -F 'kind: Secret' "$manifest" >/dev/null; then
    fail "rendered output must not contain a Secret manifest"
  fi
done

if [ -e "$output_dir" ]; then
  backup_dir=$(mktemp -d "${output_dir}.old.XXXXXX")
  rmdir "$backup_dir"
  mv -f "$output_dir" "$backup_dir"
fi
if mv -f "$staging_dir" "$output_dir"; then
  if [ -n "$backup_dir" ]; then
    rm -rf "$backup_dir"
  fi
else
  if [ -n "$backup_dir" ]; then
    mv -f "$backup_dir" "$output_dir"
  fi
  fail "could not publish rendered manifests"
fi
trap - EXIT HUP INT TERM
