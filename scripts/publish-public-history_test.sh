#!/bin/sh
# shellcheck disable=SC2016
set -eu

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd -P)
publisher=$script_dir/publish-public-history.sh

command -v git >/dev/null 2>&1 || fail "git is required"
command -v go >/dev/null 2>&1 || fail "Go 1.26.6 is required"
command -v mktemp >/dev/null 2>&1 || fail "mktemp is required"

version_output=$(go version 2>/dev/null) || fail "Go 1.26.6 is required"
case "$version_output" in
  'go version go1.26.6 '*) ;;
  *) fail "Go 1.26.6 is required" ;;
esac

test_root=$(CDPATH='' cd "$(mktemp -d "${TMPDIR:-/tmp}/publish-public-history-test.XXXXXX")" && pwd -P)
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "${PUBLISH_PUBLIC_KEEP_TEMP:-0}" = 1 ]; then
    printf 'Publish-history test diagnostics retained at %s\n' "$test_root" >&2
  else
    rm -rf "$test_root"
  fi
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

fixture_commit() {
  git -C "$1" -c user.name='fixture' -c user.email='fixture@example.invalid' \
    commit -q --no-gpg-sign -m "$2"
}

make_fixture() {
  fixture=$1
  mkdir -p "$fixture/secrets" "$fixture/scripts"
  git -C "$fixture" init -q -b main
  printf 'module fixture\n\ngo 1.26\n' >"$fixture/go.mod"
  printf 'package fixture\n' >"$fixture/fixture.go"
  printf 'public fixture\n' >"$fixture/public.txt"
  printf 'INDEX01_WEBHOOK_TOKEN=\n' >"$fixture/.env.example"
  printf 'INDEX01_WEBHOOK_TOKEN=private\n' >"$fixture/deploy.env"
  printf 'private\n' >"$fixture/secrets/config.yaml"
  printf 'private\n' >"$fixture/application.log"
  cp "$script_dir/clean-public-source.sh" "$fixture/scripts/clean-public-source.sh"
  cp "$publisher" "$fixture/scripts/publish-public-history.sh"
  git -C "$fixture" add .
  fixture_commit "$fixture" 'fixture commit'
}

# 1. A clean fixture produces a verified single-commit candidate.
fixture_dir=$test_root/fixture
make_fixture "$fixture_dir"
fixture_head=$(git -C "$fixture_dir" rev-parse HEAD)
candidate_dir=$test_root/fixture-candidate
"$fixture_dir/scripts/publish-public-history.sh" "$candidate_dir" "$fixture_dir" \
  >"$test_root/fixture-run.log" 2>&1 ||
  fail "fixture publication candidate failed"

[ "$(git -C "$candidate_dir" rev-list --count HEAD)" = 1 ] ||
  fail "candidate history is not a single root commit"
[ -z "$(git -C "$candidate_dir" rev-parse HEAD^@)" ] ||
  fail "candidate root commit has parents"
candidate_refs=$(git -C "$candidate_dir" for-each-ref --format='%(refname)')
[ "$candidate_refs" = "refs/heads/main" ] ||
  fail "candidate has unexpected refs: $candidate_refs"
[ -z "$(git -C "$candidate_dir" remote)" ] ||
  fail "candidate has a remote configured"
root_message=$(git -C "$candidate_dir" log -1 --format=%B)
case "$root_message" in
  *"Source-Private-Commit: $fixture_head"*) ;;
  *) fail "root commit does not record the private source commit" ;;
esac
case "$root_message" in
  *'Source-Tree-State: clean'*) ;;
  *) fail "root commit does not record the source tree state" ;;
esac
grep -F 'refs/heads/main:refs/heads/main' "$test_root/fixture-run.log" >/dev/null ||
  fail "run report does not state the approved refspec"

[ -f "$candidate_dir/public.txt" ] || fail "candidate omitted public source"
[ -f "$candidate_dir/.env.example" ] || fail "candidate omitted the environment example"
for private_path in deploy.env secrets secrets/config.yaml application.log; do
  [ ! -e "$candidate_dir/$private_path" ] && [ ! -L "$candidate_dir/$private_path" ] ||
    fail "candidate contains private path: $private_path"
done

# 2. A planted private-style hostname must fail the scan closed.
leaky_dir=$test_root/leaky
make_fixture "$leaky_dir"
printf 'db-host: private-vm.%s\n' 'w''ork' >"$leaky_dir/settings.yaml"
git -C "$leaky_dir" add settings.yaml
fixture_commit "$leaky_dir" 'plant private-style hostname'
if "$leaky_dir/scripts/publish-public-history.sh" "$test_root/leaky-candidate" "$leaky_dir" \
  >"$test_root/leaky-run.log" 2>&1; then
  fail "scan accepted a private-style hostname"
fi
grep -F 'private-value scan found matches' "$test_root/leaky-run.log" >/dev/null ||
  fail "scan failure did not report the private-value hit"

# 3. The script refuses every push-shaped invocation.
for forbidden in --mirror --push push; do
  if "$publisher" "$forbidden" "$test_root/refused-candidate" "$fixture_dir" \
    >"$test_root/refused-$forbidden.log" 2>&1; then
    fail "accepted forbidden argument: $forbidden"
  fi
  [ ! -e "$test_root/refused-candidate" ] ||
    fail "produced a candidate for forbidden argument: $forbidden"
done
grep -F 'mirror publication is forbidden' "$test_root/refused---mirror.log" >/dev/null ||
  fail "mirror refusal message is missing"
grep -F 'never pushes' "$test_root/refused---push.log" >/dev/null ||
  fail "push refusal message is missing"

# 4. A modified source tree fails closed unless the run is a dry run.
printf 'local edit\n' >>"$fixture_dir/public.txt"
if "$fixture_dir/scripts/publish-public-history.sh" "$test_root/dirty-candidate" "$fixture_dir" \
  >"$test_root/dirty-run.log" 2>&1; then
  fail "accepted a modified source tree without --dry-run"
fi
grep -F 'uncommitted changes' "$test_root/dirty-run.log" >/dev/null ||
  fail "modified-tree refusal message is missing"
"$fixture_dir/scripts/publish-public-history.sh" --dry-run "$test_root/dirty-dry-candidate" "$fixture_dir" \
  >"$test_root/dirty-dry-run.log" 2>&1 ||
  fail "dry run rejected a modified source tree"
dry_message=$(git -C "$test_root/dirty-dry-candidate" log -1 --format=%B)
case "$dry_message" in
  *'Source-Tree-State: modified'*) ;;
  *) fail "dry run did not record the modified source tree" ;;
esac
grep -F 'NOT for publication' "$test_root/dirty-dry-run.log" >/dev/null ||
  fail "dry run is not labelled as not for publication"

# 5. The real project builds a clean candidate (dry run tolerates local edits).
project_candidate=$test_root/project-candidate
"$publisher" --dry-run "$project_candidate" "$project_dir" \
  >"$test_root/project-run.log" 2>&1 ||
  fail "project publication candidate failed"
[ "$(git -C "$project_candidate" rev-list --count HEAD)" = 1 ] ||
  fail "project candidate history is not a single root commit"
for public_doc in AGENTS.md CLAUDE.md docs/public-publication.md; do
  [ -f "$project_candidate/$public_doc" ] ||
    fail "project candidate omitted $public_doc"
done
for private_path in .beads .agents .claude .codex .serena dist; do
  [ ! -e "$project_candidate/$private_path" ] && [ ! -L "$project_candidate/$private_path" ] ||
    fail "project candidate contains private path: $private_path"
done

printf '%s\n' 'PASS: public history publication candidate validation'
