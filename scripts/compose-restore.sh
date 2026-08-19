#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
compose_production=$script_dir/compose-production.sh

usage() {
	echo "Usage: $0 AGE_IDENTITY INPUT.age INPUT.age.sha256" >&2
	exit 2
}

# Portable replacement for BSD "ln -h" (GNU coreutils ln has no -h flag):
# refuse an existing destination, create the hard link, then verify the
# destination is not a symbolic link.
link_no_follow() {
	if [ -e "$2" ] || [ -L "$2" ]; then
		return 1
	fi
	ln -- "$1" "$2" || return 1
	[ ! -L "$2" ]
}

[ "$#" -eq 3 ] || usage
age_identity=$1
input=$2
checksum_file=$3

[ -f "$age_identity" ] || { echo "AGE_IDENTITY file does not exist" >&2; exit 1; }
[ -f "$input" ] || { echo "INPUT.age does not exist" >&2; exit 1; }
[ -f "$checksum_file" ] || { echo "INPUT.age.sha256 does not exist" >&2; exit 1; }
[ ! -L "$age_identity" ] && [ ! -L "$input" ] && [ ! -L "$checksum_file" ] || {
	echo "Compose restore inputs must not be symbolic links" >&2
	exit 1
}
case "$input" in
	*.age) ;;
	*) echo "INPUT.age must end with .age" >&2; exit 2 ;;
esac
[ "$checksum_file" = "$input.sha256" ] || {
	echo "checksum path must equal INPUT.age plus .sha256" >&2
	exit 2
}

for command_name in age awk basename chmod cp dirname docker ln mktemp rm shasum; do
	command -v "$command_name" >/dev/null 2>&1 || {
		echo "$command_name is required for Compose restore" >&2
		exit 1
	}
done

umask 077
temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/index-01-hook-restore.XXXXXX")
snapshot_directory=

stop_attempted=false
stop_succeeded=false
start_attempted=false
started=false
cleanup() {
	if { [ "$stop_attempted" = true ] && [ "$stop_succeeded" != true ]; } || \
		{ [ "$start_attempted" = true ] && [ "$started" != true ]; }; then
		"$compose_production" stop index-01-hook \
			>/dev/null 2>/dev/null || true
	fi
	rm -rf -- "$temporary_directory"
	if [ -n "${snapshot_directory:-}" ]; then
		rm -rf -- "$snapshot_directory"
	fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

plaintext_path=$temporary_directory/backup.sqlite
input_copy=$temporary_directory/backup.age
checksum_copy=$temporary_directory/backup.age.sha256
chmod 0700 "$temporary_directory"
input_directory=$(dirname -- "$input")
[ "$(dirname -- "$checksum_file")" = "$input_directory" ] || {
	echo "Compose restore inputs must share one directory" >&2
	exit 1
}
snapshot_directory=$(mktemp -d "$input_directory/.index-01-hook-restore-snapshot.XXXXXX")
chmod 0700 "$snapshot_directory"
link_no_follow "$input" "$snapshot_directory/backup.age"
link_no_follow "$checksum_file" "$snapshot_directory/backup.age.sha256"
[ ! -L "$snapshot_directory/backup.age" ] && [ ! -L "$snapshot_directory/backup.age.sha256" ] || {
	echo "Compose restore snapshot is invalid" >&2
	exit 1
}
cp -f -- "$snapshot_directory/backup.age" "$input_copy"
cp -f -- "$snapshot_directory/backup.age.sha256" "$checksum_copy"
chmod 0600 "$input_copy" "$checksum_copy"
: >"$plaintext_path"
chmod 0600 "$plaintext_path"

# Stop before any decryption or restore. Never start the application in cleanup.
stop_attempted=true
if ! "$compose_production" stop index-01-hook \
	>/dev/null 2>/dev/null; then
	echo "Could not stop the Compose application" >&2
	exit 1
fi
stop_succeeded=true

input_name=$(basename -- "$input")
expected_checksum=$(awk -v name="$input_name" '
	NF == 2 && length($1) == 64 && $1 !~ /[^0-9a-f]/ && $2 == name {
		checksum = $1
		matches++
	}
	END {
		if (NR != 1 || matches != 1) exit 1
		print checksum
	}
' "$checksum_copy") || {
	echo "Checksum file is invalid" >&2
	exit 1
}
if ! actual_checksum_output=$(shasum -a 256 "$input_copy"); then
	echo "Encrypted backup checksum calculation failed" >&2
	exit 1
fi
if ! actual_checksum=$(printf '%s\n' "$actual_checksum_output" | awk '
	{
		if (length($1) != 64 || $1 ~ /[^0-9a-f]/) invalid = 1
		if (length($1) == 64 && $1 !~ /[^0-9a-f]/) {
			digest = $1
			digests++
		}
	}
	END {
		if (NR != 1 || invalid || digests != 1) exit 1
		print digest
	}
'); then
	echo "Encrypted backup checksum output is invalid" >&2
	exit 1
fi
[ "$actual_checksum" = "$expected_checksum" ] || {
	echo "Encrypted backup checksum does not match" >&2
	exit 1
}

if ! age --decrypt --identity "$age_identity" <"$input_copy" >"$plaintext_path" \
	2>/dev/null; then
	echo "Backup decryption failed" >&2
	exit 1
fi
chmod 0600 "$plaintext_path"

if ! restore_output=$("$compose_production" run --rm --no-deps -T \
	--entrypoint /index-01-hook index-01-hook-maintenance restore - \
	<"$plaintext_path" 2>/dev/null); then
	echo "Compose database restore failed" >&2
	exit 1
fi
if [ "$restore_output" != '{"state":"restored"}' ]; then
	echo "Compose database restore returned an invalid result" >&2
	exit 1
fi

start_attempted=true
if ! "$compose_production" up -d --wait --wait-timeout 60 index-01-hook \
	>/dev/null 2>/dev/null; then
	echo "Could not start and verify the Compose application" >&2
	exit 1
fi
started=true

printf 'Restored encrypted backup: %s\n' "$input"
