#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
builder=$script_dir/build-release-artifacts.sh
project_root=$(CDPATH='' cd "$script_dir/.." && pwd -P)
test_root=$(CDPATH='' cd "$(mktemp -d "${TMPDIR:-/tmp}/build-release-artifacts-test.XXXXXX")" && pwd -P)
trap 'rm -rf -- "$test_root"' EXIT HUP INT TERM

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

version=v9.8.7
commit=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
build_date=2026-08-13T12:00:00Z
output_dir=$test_root/release

"$builder" "$version" "$commit" "$build_date" "$output_dir"

expected_names=$(printf '%s\n' \
  "index-01-hook_${version}_darwin_amd64" \
  "index-01-hook_${version}_darwin_arm64" \
  "index-01-hook_${version}_linux_amd64" \
  "index-01-hook_${version}_linux_arm64" \
  THIRD_PARTY_NOTICES.txt \
  checksums.txt | LC_ALL=C sort)
actual_names=$(find "$output_dir" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; | LC_ALL=C sort)
[ "$actual_names" = "$expected_names" ] || fail "output contains unexpected or stale files"

for target in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
  binary=$output_dir/index-01-hook_${version}_${target}
  [ -x "$binary" ] || fail "binary is not executable: $target"
  go version -m "$binary" >"$test_root/$target.version"
  grep -F "GOOS=${target%_*}" "$test_root/$target.version" >/dev/null || fail "wrong GOOS metadata: $target"
  grep -F "GOARCH=${target#*_}" "$test_root/$target.version" >/dev/null || fail "wrong GOARCH metadata: $target"
  strings "$binary" | grep -F "$version" >/dev/null || fail "version metadata is missing: $target"
  strings "$binary" | grep -F "$commit" >/dev/null || fail "commit metadata is missing: $target"
  strings "$binary" | grep -F "$build_date" >/dev/null || fail "build date metadata is missing: $target"
  if strings "$binary" | grep -F "$project_root" >/dev/null; then
    fail "binary contains an absolute project source path: $target"
  fi
done

(cd "$output_dir" && shasum -a 256 -c checksums.txt >/dev/null)
sorted_checksums=$(LC_ALL=C sort "$output_dir/checksums.txt")
[ "$sorted_checksums" = "$(cat "$output_dir/checksums.txt")" ] || fail "checksums are not sorted"
[ "$(wc -l <"$output_dir/checksums.txt")" -eq 5 ] || fail "checksum count is not five"
grep -F 'Third-Party License Texts' "$output_dir/THIRD_PARTY_NOTICES.txt" >/dev/null ||
  fail "generated third-party notices are missing"
grep -F 'Targets: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64' \
  "$output_dir/THIRD_PARTY_NOTICES.txt" >/dev/null ||
  fail "generated notices do not cover all release targets"

# Identical inputs produce identical binaries, notices, and checksums.
second_output=$test_root/release-second
"$builder" "$version" "$commit" "$build_date" "$second_output"
[ "$(shasum -a 256 "$output_dir/checksums.txt" | awk '{print $1}')" = \
  "$(shasum -a 256 "$second_output/checksums.txt" | awk '{print $1}')" ] ||
  fail "identical metadata did not produce deterministic checksums"

# Existing output is immutable and cannot be replaced.
old_listing=$(find "$output_dir" -mindepth 1 -maxdepth 1 -exec basename {} \; | LC_ALL=C sort)
if "$builder" "$version" "$commit" "$build_date" "$output_dir" >"$test_root/existing.stdout" 2>"$test_root/existing.stderr"; then
  fail "existing release output was replaced"
fi
[ "$(find "$output_dir" -mindepth 1 -maxdepth 1 -exec basename {} \; | LC_ALL=C sort)" = "$old_listing" ] ||
  fail "existing output changed"

# The operating-system no-replace rename cannot replace a concurrent destination.
rename_source=$test_root/rename-source
rename_destination=$test_root/rename-destination
mkdir -p "$rename_source" "$rename_destination"
printf source >"$rename_source/value"
printf destination >"$rename_destination/value"
if (cd "$project_root" && GOTOOLCHAIN=local go run ./scripts/rename-no-replace \
  "$rename_source" "$rename_destination") >"$test_root/rename.stdout" 2>"$test_root/rename.stderr"; then
  fail "no-replace rename replaced an existing destination"
fi
[ "$(cat "$rename_source/value")" = source ] || fail "no-replace rename changed its source"
[ "$(cat "$rename_destination/value")" = destination ] || fail "no-replace rename changed its destination"

# A failed build publishes no output directory.
fake_bin=$test_root/fake-bin
mkdir -p "$fake_bin"
cat >"$fake_bin/go" <<'FAKE_GO'
#!/bin/sh
exit 77
FAKE_GO
chmod 0755 "$fake_bin/go"
failed_output=$test_root/failed-release
if PATH="$fake_bin:$PATH" "$builder" "$version" "$commit" "$build_date" "$failed_output" >"$test_root/failure.stdout" 2>"$test_root/failure.stderr"; then
  fail "build failure was accepted"
fi
[ ! -e "$failed_output" ] || fail "failed build published an output directory"

expect_reject_path() {
  name=$1
  path=$2
  if "$builder" "$version" "$commit" "$build_date" "$path" >"$test_root/$name.stdout" 2>"$test_root/$name.stderr"; then
    fail "accepted unsafe output path: $name"
  fi
}

expect_reject_path whitespace "$test_root/unsafe path"
expect_reject_path newline "$test_root/unsafe
path"
expect_reject_path shell-syntax "$test_root/unsafe;id"
expect_reject_path traversal "$test_root/one/../two"
expect_reject_path project-root "$project_root"
expect_reject_path project-root-dot .
expect_reject_path source-directory "$project_root/scripts"
expect_reject_path other-project-directory "$project_root/docs/release-output"
mkdir -p "$test_root/real-output"
ln -s "$test_root/real-output" "$test_root/output-link"
expect_reject_path output-symlink "$test_root/output-link"
ln -s "$test_root" "$test_root/parent-link"
expect_reject_path parent-symlink "$test_root/parent-link/child"

printf '%s\n' 'PASS: release artifact generation'
