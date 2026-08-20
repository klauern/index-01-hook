#!/bin/sh
# shellcheck disable=SC2016
# Build a publication candidate for the public repository: a brand-new Git
# history whose single root commit contains only the sanitized public source
# tree produced by scripts/clean-public-source.sh.
#
# This script NEVER pushes and NEVER configures a remote. Publication is a
# manual, owner-approved step using the exact refspec printed at the end.
# Mirror pushes are forbidden because they would copy every ref of whatever
# repository they run in. See docs/public-publication.md.
set -eu

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

usage() {
  cat >&2 <<'EOF'
usage: publish-public-history.sh [--dry-run] [--sign] [OUTPUT_DIR] [PROJECT_DIR]

Builds a fresh single-commit public repository candidate from the sanitized
export and verifies it inside a clean clone. Never adds remotes, never pushes.

  --dry-run   Report-only run: allows a modified source tree (recorded in the
              root commit) and labels the candidate as not for publication.
  --sign      GPG-sign the root commit. The real publication candidate must be
              signed; see docs/public-publication.md.
EOF
  exit 2
}

dry_run=0
sign_root=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run) dry_run=1 ;;
    --sign) sign_root=1 ;;
    -h|--help) usage ;;
    *--mirror*)
      fail "mirror publication is forbidden: it would copy every ref of this repository; the only approved refspec is refs/heads/main:refs/heads/main"
      ;;
    --push|push)
      fail "this script never pushes; publication is a manual owner-approved action (see docs/public-publication.md)"
      ;;
    --*) usage ;;
    *) break ;;
  esac
  shift
done
[ "$#" -le 2 ] || usage

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
project_dir=${2:-$script_dir/..}
project_dir=$(CDPATH='' cd "$project_dir" && pwd -P) || fail "project directory does not exist"

command -v git >/dev/null 2>&1 || fail "git is required"
command -v go >/dev/null 2>&1 || fail "go is required"
command -v grep >/dev/null 2>&1 || fail "grep is required"
command -v mktemp >/dev/null 2>&1 || fail "mktemp is required"

git -C "$project_dir" rev-parse --verify HEAD >/dev/null 2>&1 ||
  fail "project has no commit to record as the history boundary"
source_commit=$(git -C "$project_dir" rev-parse HEAD)
if [ -n "$(git -C "$project_dir" status --porcelain)" ]; then
  source_tree_state=modified
  [ "$dry_run" = 1 ] ||
    fail "source tree has uncommitted changes; commit them first or use --dry-run"
else
  source_tree_state=clean
fi
cut_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)

if [ "$#" -ge 1 ]; then
  candidate_dir=$1
  case "$candidate_dir" in
    /*) ;;
    *) candidate_dir=$(pwd -P)/$candidate_dir ;;
  esac
else
  candidate_dir=$(mktemp -d "${TMPDIR:-/tmp}/index01-public-candidate.XXXXXX")/repo
fi

work_root=$(CDPATH='' cd "$(mktemp -d "${TMPDIR:-/tmp}/index01-publish-public.XXXXXX")" && pwd -P)
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "${PUBLISH_PUBLIC_KEEP_TEMP:-0}" = 1 ]; then
    printf 'Publish diagnostics retained at %s\n' "$work_root" >&2
  else
    rm -rf "$work_root"
  fi
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

run_step() {
  name=$1
  shift
  if ! "$@" >"$work_root/$name.log" 2>&1; then
    tail -n 20 "$work_root/$name.log" | sed 's/^/  /' >&2 || true
    fail "$name failed"
  fi
}

# 1. Export the sanitized public source tree.
run_step sanitized-export "$script_dir/clean-public-source.sh" "$candidate_dir" "$project_dir"

# 2. Create the fresh public history: one root commit, branch main, no remote.
git -C "$candidate_dir" init -q -b main
publisher_name=${INDEX01_PUBLISHER_NAME:-$(git -C "$project_dir" config user.name 2>/dev/null || echo 'index-01-hook publisher')}
publisher_email=${INDEX01_PUBLISHER_EMAIL:-$(git -C "$project_dir" config user.email 2>/dev/null || echo 'publisher@invalid')}
if [ "$sign_root" = 1 ]; then
  sign_flag=--gpg-sign
else
  sign_flag=--no-gpg-sign
fi
commit_message="chore: initialize public history from sanitized export

This repository begins at a deliberate history boundary. The tree of this
root commit is the sanitized public export of the private source repository;
no earlier commits are published. Prior development history, auxiliary refs,
and unreachable objects are retained only in the private archive repository.

Source-Private-Commit: $source_commit
Source-Tree-State: $source_tree_state
Cut-Date: $cut_date"
run_step candidate-add git -C "$candidate_dir" add --all --force
run_step candidate-commit git -C "$candidate_dir" \
  -c user.name="$publisher_name" \
  -c user.email="$publisher_email" \
  commit -q "$sign_flag" -m "$commit_message"

root_commit=$(git -C "$candidate_dir" rev-parse HEAD)
[ "$(git -C "$candidate_dir" rev-list --count HEAD)" = 1 ] ||
  fail "candidate history must contain exactly one commit"
candidate_refs=$(git -C "$candidate_dir" for-each-ref --format='%(refname)')
[ "$candidate_refs" = "refs/heads/main" ] ||
  fail "candidate contains unexpected refs: $candidate_refs"
[ -z "$(git -C "$candidate_dir" remote)" ] ||
  fail "candidate must not have any remote configured"

# 3. Verify inside a clean clone so only committed content is checked.
clone_dir=$work_root/verify-clone
run_step verification-clone git clone -q --no-hardlinks "$candidate_dir" "$clone_dir"
git -C "$clone_dir" remote remove origin

for private_path in .beads .agents .claude .codex .serena dist .env secrets backup backups logs; do
  if [ -e "$clone_dir/$private_path" ] || [ -L "$clone_dir/$private_path" ]; then
    fail "private path entered the candidate: $private_path"
  fi
done

[ -f "$clone_dir/go.mod" ] || fail "candidate is missing go.mod"
run_step go-vet sh -c 'cd "$1" && GOTOOLCHAIN=local GOPROXY=off go vet ./...' sh "$clone_dir"
run_step go-test sh -c 'cd "$1" && GOTOOLCHAIN=local GOPROXY=off go test ./...' sh "$clone_dir"

# 4. Private-value scan of the clone. Patterns bracket their first letter so
# this script never contains the private literals it rejects while the scan
# still matches the real values. Fails closed on any hit.
scan_hits=$work_root/scan-hits.txt
scan_errors=$work_root/scan-errors.txt
if grep -rEn --binary-files=without-match --exclude-dir=.git \
  -e '[a-z0-9-]+(\.[a-z0-9-]+)*\.[w]ork([^a-zA-Z0-9-]|$)' \
  -e 'registry-[0-9]+\.[d]ocker\.io' \
  -e '[g]itea[_-]admin' \
  -e 'BEGIN[[:space:]]+([A-Z]+[[:space:]]+)*[P]RIVATE[[:space:]]+KEY' \
  -e '[A]KIA[0-9A-Z]{16}' \
  -e '[g]hp_[A-Za-z0-9]{36}' \
  -e '[g]ho_[A-Za-z0-9]{36}' \
  -e '[g]ithub_pat_[A-Za-z0-9_]{22}' \
  -e '[x]ox[abprs]-[0-9A-Za-z-]{10}' \
  -- "$clone_dir" >"$scan_hits" 2>"$scan_errors"; then
  sed 's/^/  /' "$scan_hits" >&2
  fail "private-value scan found matches in the candidate"
fi
[ ! -s "$scan_errors" ] || {
  sed 's/^/  /' "$scan_errors" >&2
  fail "private-value scan could not run"
}

# 5. Report. Publication itself is manual and owner-approved.
mode_label='publication candidate'
[ "$dry_run" = 1 ] && mode_label='dry run (NOT for publication)'
cat <<EOF
PASS: public history candidate built and verified

  Mode                 : $mode_label
  Candidate repository : $candidate_dir
  Candidate root commit: $root_commit
  Source private commit: $source_commit (source tree: $source_tree_state)
  Cut date             : $cut_date
  Ref inventory        : refs/heads/main (only)
  Verification         : clean clone; go vet, go test, private-value scan

This script does not push and does not configure remotes. Publication is a
manual step that requires owner approval. After approval the owner runs:

  git -C '$candidate_dir' push <public-remote-url> refs/heads/main:refs/heads/main

The ONLY approved refspec is refs/heads/main:refs/heads/main. Never publish
with a mirror push (it would copy every ref), and never push from the
private archive repository.
EOF
