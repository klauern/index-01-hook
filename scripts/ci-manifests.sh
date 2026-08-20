#!/bin/sh
set -eu

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

command -v kubeconform >/dev/null 2>&1 || fail "kubeconform is required"

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd)
umask 077
test_root=$(mktemp -d "${TMPDIR:-/tmp}/ci-manifests.XXXXXX")
cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	rm -rf "$test_root"
	exit "$status"
}
trap cleanup EXIT HUP INT TERM

image_digest=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
image_ref=registry.example/index-01-hook@sha256:$image_digest
expected_files='namespace.yaml service-account.yaml service.yaml network-policy.yaml deployment.yaml maintenance-pod.yaml ingress.yaml pvc.yaml'

validate_set() {
	mode=$1
	storage_class=$2
	output_dir=$test_root/$mode

	REGISTRY_ACCESS_MODE=$mode \
	KUBE_INGRESS_HOST=hook.example.test \
	KUBE_INGRESS_CLASS=standard \
	KUBE_TLS_SECRET=index-01-hook-tls \
	KUBE_STORAGE_CLASS=$storage_class \
		"$script_dir/render-manifests.sh" "$image_ref" "$output_dir"

	for manifest in $expected_files; do
		[ -f "$output_dir/$manifest" ] || fail "missing $mode manifest: $manifest"
	done

	actual_count=0
	for entry in "$output_dir"/* "$output_dir"/.[!.]* "$output_dir"/..?*; do
		[ -e "$entry" ] || continue
		if [ ! -f "$entry" ] || [ -L "$entry" ]; then
			fail "$mode output contains a non-file entry: $(basename "$entry")"
		fi
		case " $expected_files " in
			*" $(basename "$entry") "*) ;;
			*) fail "unexpected $mode manifest: $(basename "$entry")" ;;
		esac
		actual_count=$((actual_count + 1))
	done
	[ "$actual_count" -eq 8 ] || fail "expected exactly eight $mode manifests"

	for manifest in $expected_files; do
		path=$output_dir/$manifest
		if grep -Eiq 'placeholder|replace_with|changeme|todo' "$path"; then
			fail "$mode manifest contains a placeholder: $manifest"
		fi
		if grep -Eiq '^[[:space:]]*kind:[[:space:]]*Secret[[:space:]]*$' "$path"; then
			fail "$mode output contains a Secret manifest: $manifest"
		fi
	done

	if [ "$mode" = public ]; then
		! grep -F 'imagePullSecrets:' "$output_dir/deployment.yaml" >/dev/null ||
			fail "public deployment contains imagePullSecrets"
		! grep -F 'storageClassName:' "$output_dir/pvc.yaml" >/dev/null ||
			fail "public default-storage PVC contains storageClassName"
	else
		grep -F 'imagePullSecrets:' "$output_dir/deployment.yaml" >/dev/null ||
			fail "private deployment has no imagePullSecrets"
		grep -F 'storageClassName: standard' "$output_dir/pvc.yaml" >/dev/null ||
			fail "private explicit-storage PVC has the wrong storage class"
	fi

	kubeconform -strict -summary -kubernetes-version 1.31.0 "$output_dir"/*.yaml
}

validate_set public ''
validate_set private standard
printf '%s\n' 'PASS: public and private Kubernetes manifests match the supported eight-file sets'
