#!/bin/sh
# shellcheck disable=SC2016
set -eu

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
project_dir=$(CDPATH='' cd "$script_dir/.." && pwd -P)
command -v git >/dev/null 2>&1 || fail "git is required"
command -v go >/dev/null 2>&1 || fail "Go 1.26.6 is required"
command -v mktemp >/dev/null 2>&1 || fail "mktemp is required"
command -v docker >/dev/null 2>&1 || fail "Docker Compose is required"
docker compose version >/dev/null 2>&1 || fail "Docker Compose is required"

version_output=$(go version 2>/dev/null) || fail "Go 1.26.6 is required"
case "$version_output" in
  'go version go1.26.6 '*) ;;
  *) fail "Go 1.26.6 is required" ;;
esac

root=$(CDPATH='' cd "$(mktemp -d "${TMPDIR:-/tmp}/index01-clean-source.XXXXXX")" && pwd -P)
export_dir=$root/export

task_state=skipped
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "${CLEAN_PUBLIC_KEEP_TEMP:-0}" = 1 ]; then
    printf 'Clean-source diagnostics retained at %s\n' "$root" >&2
  else
    rm -rf "$root"
  fi
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

fixture_dir=$root/fixture
fixture_export=$root/fixture-export
mkdir -p "$fixture_dir/secrets" "$fixture_dir/backup" \
  "$fixture_dir/.beads" "$fixture_dir/.agents" "$fixture_dir/.claude" \
  "$fixture_dir/.codex" "$fixture_dir/.serena" \
  "$fixture_dir/deploy/backup" "$fixture_dir/scripts/logs"
git -C "$fixture_dir" init -q
printf 'public fixture\n' >"$fixture_dir/public.txt"
printf 'public fixture\n' >"$fixture_dir/deploy/manifest.yaml"
printf 'empty\n' >"$fixture_dir/.env.example"
printf 'private\n' >"$fixture_dir/deploy.env"
printf 'private\n' >"$fixture_dir/config.secret.yaml"
printf 'private\n' >"$fixture_dir/secrets/config.yaml"
printf 'private\n' >"$fixture_dir/backup/snapshot.db-journal"
printf 'private\n' >"$fixture_dir/cache.sqlite3-wal"
printf 'private\n' >"$fixture_dir/application.log"
printf 'private\n' >"$fixture_dir/.beads/issues.jsonl"
printf 'private\n' >"$fixture_dir/.agents/skills.md"
printf 'private\n' >"$fixture_dir/.claude/settings.local.json"
printf 'private\n' >"$fixture_dir/.codex/config.toml"
printf 'private\n' >"$fixture_dir/.serena/project.yml"
printf 'private\n' >"$fixture_dir/server.key"
printf 'private\n' >"$fixture_dir/ca.pem"
printf 'private\n' >"$fixture_dir/deploy/backup/snapshot.sql"
printf 'private\n' >"$fixture_dir/scripts/logs/debug.log"
printf 'private\n' >"$fixture_dir/id_rsa"
printf 'private\n' >"$fixture_dir/id_ed25519"
printf 'private\n' >"$fixture_dir/id_ecdsa"
# Force-add so a global gitignore cannot make the exclusion checks vacuous.
git -C "$fixture_dir" add -f .
"$script_dir/clean-public-source.sh" "$fixture_export" "$fixture_dir" >"$root/fixture-export.log" 2>&1 ||
  fail "fixture public source export failed"
[ -f "$fixture_export/public.txt" ] || fail "fixture omitted public source"
[ -f "$fixture_export/deploy/manifest.yaml" ] || fail "fixture omitted nested public source"
[ -f "$fixture_export/.env.example" ] || fail "fixture omitted public environment example"
for private_path in deploy.env config.secret.yaml secrets backup/snapshot.db-journal \
  cache.sqlite3-wal application.log .beads .agents .claude .codex .serena \
  server.key ca.pem deploy/backup scripts/logs id_rsa id_ed25519 id_ecdsa; do
  if [ -e "$fixture_export/$private_path" ] || [ -L "$fixture_export/$private_path" ]; then
    fail "fixture exported private path: $private_path"
  fi
done

"$script_dir/clean-public-source.sh" "$export_dir" "$project_dir" >"$root/export.log" 2>&1 ||
  fail "public source export failed"

for private_path in .beads .agents .claude .codex .serena backup backups logs coverage tmp out build generated; do
  if [ -e "$export_dir/$private_path" ] || [ -L "$export_dir/$private_path" ]; then
    fail "local state entered the public export: $private_path"
  fi
done

for public_doc in AGENTS.md CLAUDE.md; do
  [ -f "$export_dir/$public_doc" ] ||
    fail "contributor guidance missing from the public export: $public_doc"
done

# The .env.example carve-out must override the environment-file exclusions.
[ -f "$export_dir/.env.example" ] ||
  fail "public environment example missing from the export"


run_step() {
  name=$1
  shift
  if ! "$@" >"$root/$name.log" 2>&1; then
    fail "$name failed"
  fi
}

run_step go-test sh -c 'cd "$1" && GOTOOLCHAIN=local GOPROXY=off go test ./...' sh "$export_dir"
run_step documentation-tests sh -c 'cd "$1" && GOTOOLCHAIN=local GOPROXY=off go test . -run "TestMarkdownRelativeLinks|TestREADMEReferenceLinks|TestExampleEnvironmentValuesAreEmpty|TestComposeSmokeWebhookIsProviderFree" -count=1' sh "$export_dir"

if command -v task >/dev/null 2>&1; then
  run_step task-test env GOTOOLCHAIN=local GOPROXY=off task --silent --dir "$export_dir" test
  task_state=ran
fi

# Empty required values must fail Compose before it produces a rendered model.
compose_output=$root/compose-config.yaml
if docker compose --env-file "$export_dir/.env.example" -f "$export_dir/compose.yaml" config \
  >"$compose_output" 2>"$root/compose-config.log"; then
  fail "Compose accepted empty required values"
fi
[ ! -s "$compose_output" ] || fail "Compose rendered output from empty required values"

run_step release-tag-tests sh -c 'cd "$1" && scripts/validate-release-tag_test.sh' sh "$export_dir"
run_step release-artifact-tests sh -c 'cd "$1" && scripts/build-release-artifacts_test.sh' sh "$export_dir"

release_dir=$root/release-artifacts
release_version=v9.8.7
release_commit=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
release_date=2026-08-19T00:00:00Z
run_step release-artifacts "$export_dir/scripts/build-release-artifacts.sh" \
  "$release_version" "$release_commit" "$release_date" "$release_dir"

expected_names=$(printf '%s\n' \
  "index-01-hook_${release_version}_darwin_amd64" \
  "index-01-hook_${release_version}_darwin_arm64" \
  "index-01-hook_${release_version}_linux_amd64" \
  "index-01-hook_${release_version}_linux_arm64" \
  THIRD_PARTY_NOTICES.txt checksums.txt | LC_ALL=C sort)
actual_names=$(find "$release_dir" -mindepth 1 -maxdepth 1 -type f -exec basename {} \; | LC_ALL=C sort)
[ "$actual_names" = "$expected_names" ] || fail "release artifact set is incomplete"
run_step release-checksums sh -c 'cd "$1" && shasum -a 256 -c checksums.txt' sh "$release_dir"
grep -F 'Third-Party License Texts' "$release_dir/THIRD_PARTY_NOTICES.txt" >"$root/notice-check.log" 2>&1 ||
  fail "release notice report is incomplete"
grep -F 'Targets: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64' \
  "$release_dir/THIRD_PARTY_NOTICES.txt" >"$root/target-notice-check.log" 2>&1 ||
  fail "release notice report has incomplete targets"

# Check examples and generated artifacts for private deployment values.
for example in "$export_dir/.env.example" "$export_dir/deploy/kubernetes/index-01-hook-secrets.env.example"; do
  [ -f "$example" ] || fail "public environment example is missing"
  if awk -F= '/^INDEX01_(WEBHOOK_TOKEN|DEEPSEEK_TOKEN|TICKTICK_TOKEN|TICKTICK_DEFAULT_PROJECT_ID|TICKTICK_NOTE_PROJECT_ID)=/ { if ($2 != "") exit 1 }' "$example"; then
    :
  else
    fail "public environment example contains a deployment value"
  fi
done
[ ! -e "$export_dir/.env" ] || fail "private Compose environment file entered the export"
[ ! -e "$export_dir/deploy/kubernetes/index-01-hook-secrets.env" ] ||
  fail "private Kubernetes environment file entered the export"
for artifact in "$release_dir"/*; do
  if grep -aE 'INDEX01_(WEBHOOK_TOKEN|DEEPSEEK_TOKEN|TICKTICK_TOKEN|TICKTICK_DEFAULT_PROJECT_ID|TICKTICK_NOTE_PROJECT_ID)=[^[:space:]]+' \
    "$artifact" >"$root/private-value-check.log" 2>&1; then
    fail "release artifact contains a private deployment value"
  fi
done

if [ "$task_state" = ran ]; then
  echo "PASS: clean public source validation (Task test ran)"
else
  echo "PASS: clean public source validation (Task test skipped)"
fi
