#!/bin/sh
set -eu

fail() {
  echo "$1" >&2
  exit 1
}

[ "$#" -eq 4 ] || fail "usage: $0 VERSION COMMIT BUILD_DATE OUTPUT_DIR"
requested_version=$1
commit=$2
build_date=$3
output_dir=$4

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
project_root=$(CDPATH='' cd "$script_dir/.." && pwd -P)
tag_validator=$script_dir/validate-release-tag.sh
input_validator=$script_dir/validate-release-inputs.sh

version=$("$tag_validator" "$requested_version")
"$input_validator" metadata "$version" "$commit" "$build_date"

case "$output_dir" in
  ''|*[!ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._/-]*)
    fail "OUTPUT_DIR contains whitespace or unsafe characters"
    ;;
  */)
    fail "OUTPUT_DIR must name a directory"
    ;;
esac

case "$output_dir" in
  ..|../*|*/..|*/../*)
    fail "OUTPUT_DIR must not contain parent traversal"
    ;;
esac

# Resolve the path without following symlink components.
case "$output_dir" in
  /*) lexical_output=$output_dir ;;
  *) lexical_output=$(pwd -P)/$output_dir ;;
esac
check_path=$lexical_output
case "$check_path" in
  /*) current_path=/; remainder=${check_path#/} ;;
  *) current_path=.; remainder=$check_path ;;
esac
while [ -n "$remainder" ]; do
  component=${remainder%%/*}
  component_path=$current_path/$component
  # macOS commonly exposes the private temporary directory through /tmp.
  case "$component_path" in
    /tmp|//tmp) ;;
    *)
      if [ "$component" != '' ] && [ "$component" != . ] && [ -L "$component_path" ]; then
        fail "OUTPUT_DIR must not contain symlink components"
      fi
      ;;
  esac
  current_path=$current_path/$component
  if [ "$remainder" = "$component" ]; then
    remainder=
  else
    remainder=${remainder#*/}
  fi
done

output_parent=$(dirname "$output_dir")
mkdir -p "$output_parent"
[ ! -L "$output_dir" ] || fail "OUTPUT_DIR must not be a symlink"
[ ! -e "$output_dir" ] || fail "OUTPUT_DIR must not already exist"

output_parent_abs=$(CDPATH='' cd "$output_parent" && pwd -P)
output_name=$(basename "$output_dir")
output_abs=$output_parent_abs/$output_name
if [ -d "$output_abs" ]; then
  output_abs=$(CDPATH='' cd "$output_abs" && pwd -P)
fi

same_or_child() {
  target=$1
  root=$2
  [ "$target" = "$root" ] || case "$target" in "$root"/*) return 0 ;; *) return 1 ;; esac
}

# Keep generated artifacts outside the project source directories.
[ "$output_abs" != "$project_root" ] ||
  fail "OUTPUT_DIR must not be the project root or a source directory"
if same_or_child "$output_abs" "$project_root"; then
  same_or_child "$output_abs" "$project_root/dist" ||
    fail "OUTPUT_DIR inside the project must be under dist"
fi
for source_dir in \
  "$project_root/scripts" \
  "$project_root/migrations" \
  "$project_root/deploy" \
  "$project_root/.git" \
  "$project_root/.beads"; do
  source_dir_abs=$(CDPATH='' cd "$source_dir" 2>/dev/null && pwd -P) || continue
  same_or_child "$output_abs" "$source_dir_abs" &&
    fail "OUTPUT_DIR must not be the project root or a source directory"
done

command -v go >/dev/null 2>&1 || fail "go is required"
if command -v shasum >/dev/null 2>&1; then
  checksum_command=shasum
else
  command -v sha256sum >/dev/null 2>&1 || fail "shasum or sha256sum is required"
  checksum_command=sha256sum
fi

staging_dir=
cleanup() {
  exit_status=$?
  if [ -n "$staging_dir" ] && [ -d "$staging_dir" ]; then
    rm -rf -- "$staging_dir"
  fi
  exit "$exit_status"
}
trap cleanup EXIT HUP INT TERM

staging_dir=$(mktemp -d "$output_parent/.index-01-hook-release.XXXXXX") ||
  fail "could not create private staging directory"
chmod 0700 "$staging_dir"

build_binary() {
  target_os=$1
  target_arch=$2
  binary_name=index-01-hook_${version}_${target_os}_${target_arch}
  (
    cd "$project_root"
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" GOTOOLCHAIN=local \
      go build -trimpath -buildvcs=false \
        -ldflags="-s -w -X main.version=$version -X main.commit=$commit -X main.buildDate=$build_date" \
        -o "$staging_dir/$binary_name" .
  )
  [ -f "$staging_dir/$binary_name" ] || fail "go build did not create $binary_name"
  chmod 0755 "$staging_dir/$binary_name"
}

build_binary linux amd64
build_binary linux arm64
build_binary darwin amd64
build_binary darwin arm64
(
  cd "$project_root"
  GOTOOLCHAIN=local go run ./scripts/release-notices \
    --version "$version" \
    --target linux/amd64 \
    --target linux/arm64 \
    --target darwin/amd64 \
    --target darwin/arm64 \
    --output "$staging_dir/THIRD_PARTY_NOTICES.txt"
)
chmod 0644 "$staging_dir/THIRD_PARTY_NOTICES.txt"

(
  cd "$staging_dir"
  for binary_name in \
    "index-01-hook_${version}_linux_amd64" \
    "index-01-hook_${version}_linux_arm64" \
    "index-01-hook_${version}_darwin_amd64" \
    "index-01-hook_${version}_darwin_arm64" \
    THIRD_PARTY_NOTICES.txt; do
    case "$checksum_command" in
      shasum) shasum -a 256 "$binary_name" ;;
      sha256sum) sha256sum "$binary_name" ;;
    esac
  done
) | LC_ALL=C sort >"$staging_dir/checksums.txt"
chmod 0644 "$staging_dir/checksums.txt"

# The destination is absent, so this same-filesystem rename publishes one
# complete directory entry without an intermediate missing state.
(
  cd "$project_root"
  GOTOOLCHAIN=local go run ./scripts/rename-no-replace "$staging_dir" "$output_dir"
) || fail "could not publish release artifacts"
staging_dir=
