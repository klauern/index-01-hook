#!/bin/sh
set -eu

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  fail "usage: $0 DESTINATION [PROJECT_DIR]"
fi

export_dir=$1
project_dir=${2:-.}

case "$export_dir" in
  /*|../*|*/../*|*/..|..) ;;
  *) export_dir=$(pwd -P)/$export_dir ;;
esac
project_dir=$(CDPATH='' cd "$project_dir" && pwd -P) || fail "project directory does not exist"

if [ -e "$export_dir" ] || [ -L "$export_dir" ]; then
  fail "destination already exists: $export_dir"
fi
mkdir -p "$export_dir"
chmod 0700 "$export_dir"

command -v git >/dev/null 2>&1 || fail "git is required"
command -v realpath >/dev/null 2>&1 || fail "realpath is required"

is_excluded() {
  path=$1
  # The public environment example overrides the environment-file exclusions.
  [ "$path" = .env.example ] && return 1
  case "$path" in
    index-01-hook|.env|.env/*|.env.?*|*.env|*secret*.yaml|*secret*.yml|*.db|*.db-shm|*.db-wal|*.db-journal|*.sqlite|*.sqlite3|*.sqlite-*|*.sqlite3-shm|*.sqlite3-wal|*.log|*.key|*.pem|*.crt|*.cer|*.p12|*.pfx|*.age|*.bak|*.backup)
      return 0
      ;;
  esac
  # Directory categories and key-material file names are excluded as path
  # segments at any depth. The wrapping slashes make every segment testable.
  case "/$path/" in
    */.git/*|*/.beads/*|*/.agents/*|*/.claude/*|*/.codex/*|*/.serena/*|*/dist/*|*/secrets/*|*/backup/*|*/backups/*|*/logs/*|*/coverage/*|*/tmp/*|*/out/*|*/build/*|*/generated/*|*/id_rsa/*|*/id_ed25519/*|*/id_ecdsa/*)
      return 0
      ;;
  esac
  return 1
}

manifest=$(mktemp "${TMPDIR:-/tmp}/index01-public-source.XXXXXX")
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  rm -f "$manifest"
  exit "$status"
}
trap cleanup EXIT HUP INT TERM

git -C "$project_dir" ls-files --cached --others --exclude-standard >"$manifest" ||
  fail "could not list public source files"

is_manifest_path() {
  grep -F -x -- "$1" "$manifest" >/dev/null 2>&1
}

while IFS= read -r relative_path; do
  [ -n "$relative_path" ] || continue
  case "$relative_path" in
    /*|../*|*/../*|*/..|..) fail "unsafe source path" ;;
  esac
  is_excluded "$relative_path" && continue
  source_path=$project_dir/$relative_path
  [ -e "$source_path" ] || [ -L "$source_path" ] || continue
  destination=$export_dir/$relative_path
  mkdir -p "$(dirname "$destination")"
  if [ -L "$source_path" ]; then
    link_target=$(readlink "$source_path") || fail "could not read source link"
    resolved_target=$(realpath "$source_path" 2>/dev/null) || fail "source link does not resolve"
    case "$resolved_target" in
      "$project_dir"/*) ;;
      *) fail "source link resolves outside the project" ;;
    esac
    target_relative=${resolved_target#"$project_dir"/}
    is_excluded "$target_relative" && fail "source link resolves to excluded data"
    is_manifest_path "$target_relative" || fail "source link target is not in the export"
    ln -s "$link_target" "$destination"
  elif [ -f "$source_path" ]; then
    cp -p -- "$source_path" "$destination"
  else
    fail "unsupported source entry: $relative_path"
  fi
done <"$manifest"

while IFS= read -r relative_path; do
  [ -n "$relative_path" ] || continue
  is_excluded "$relative_path" && continue
  source_path=$project_dir/$relative_path
  [ -L "$source_path" ] || continue
  destination=$export_dir/$relative_path
  resolved_target=$(realpath "$destination" 2>/dev/null) || fail "export link does not resolve"
  case "$resolved_target" in
    "$export_dir"/*) ;;
    *) fail "export link resolves outside the export" ;;
  esac
done <"$manifest"

deleted_manifest=$(mktemp "${TMPDIR:-/tmp}/index01-deleted-source.XXXXXX")
git -C "$project_dir" ls-files --deleted >"$deleted_manifest" || true
while IFS= read -r relative_path; do
  [ -n "$relative_path" ] || continue
  is_excluded "$relative_path" && continue
  if [ -e "$export_dir/$relative_path" ] || [ -L "$export_dir/$relative_path" ]; then
    fail "deleted tracked file reappeared in the export: $relative_path"
  fi
done <"$deleted_manifest"
rm -f "$deleted_manifest"

printf 'PASS: public source exported to %s\n' "$export_dir"
