#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd "$(dirname "$0")" && pwd -P)
compose_production=$script_dir/compose-production.sh

usage() {
	echo "Usage: $0 AGE_RECIPIENT OUTPUT.age" >&2
	exit 2
}

[ "$#" -eq 2 ] || usage
age_recipient=$1
output=$2
[ -n "$age_recipient" ] || { echo "AGE_RECIPIENT is required" >&2; exit 2; }

case "$output" in
	*.age) ;;
	*) echo "OUTPUT must end with .age" >&2; exit 2 ;;
esac

output_dir=$(dirname -- "$output")
output_name=$(basename -- "$output")
checksum=$output.sha256
checksum_name=$(basename -- "$checksum")
[ -d "$output_dir" ] || { echo "OUTPUT directory does not exist" >&2; exit 1; }

# Restrict names before creating temporary files or publishing hard links.
LC_ALL=C
export LC_ALL
case "$output_name" in
	''|*[!A-Za-z0-9._-]*)
		echo "OUTPUT basename contains unsupported characters" >&2
		exit 2
		;;
esac

for command_name in age awk basename cat dirname docker ln mkfifo mktemp rm shasum; do
	command -v "$command_name" >/dev/null 2>&1 || {
		echo "$command_name is required for Compose backup" >&2
		exit 1
	}
done

umask 077
temporary_directory=$(mktemp -d "$output_dir/.index-01-hook-backup.XXXXXX")
producer_pid=
age_pid=
pipe_path=$temporary_directory/backup.pipe
encrypted_path=$temporary_directory/$output_name
checksum_path=$temporary_directory/$checksum_name

cleanup() {
	if [ -n "$producer_pid" ]; then
		kill "$producer_pid" 2>/dev/null || true
		wait "$producer_pid" 2>/dev/null || true
	fi
	if [ -n "$age_pid" ]; then
		kill "$age_pid" 2>/dev/null || true
		wait "$age_pid" 2>/dev/null || true
	fi
	rm -rf -- "$temporary_directory"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

mkfifo -m 0600 "$pipe_path"

"$compose_production" exec -T index-01-hook \
	/index-01-hook backup - >"$pipe_path" 2>/dev/null &
producer_pid=$!

age -r "$age_recipient" -o "$encrypted_path" <"$pipe_path" 2>/dev/null &
age_pid=$!

# The encryption process reaches EOF only after the producer closes the FIFO.
# This wait also records the producer result without a pipeline.
if wait "$age_pid"; then
	age_status=0
else
	age_status=$?
fi
age_pid=

if [ "$age_status" -ne 0 ]; then
	kill "$producer_pid" 2>/dev/null || true
fi
if wait "$producer_pid"; then
	producer_status=0
else
	producer_status=$?
fi
producer_pid=

if [ "$producer_status" -ne 0 ]; then
	echo "Compose database backup failed" >&2
	exit 1
fi
if [ "$age_status" -ne 0 ]; then
	echo "Backup encryption failed" >&2
	exit 1
fi

chmod 0600 "$encrypted_path"
if ! checksum_output=$(shasum -a 256 "$encrypted_path"); then
	echo "Backup checksum calculation failed" >&2
	exit 1
fi
if ! checksum_value=$(printf '%s\n' "$checksum_output" | awk '
	{
		if (length($1) != 64 || $1 ~ /[^0-9a-f]/) {
			invalid = 1
		}
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
	echo "Backup checksum output is invalid" >&2
	exit 1
fi
printf '%s  %s\n' "$checksum_value" "$output_name" >"$checksum_path"
chmod 0600 "$checksum_path"

# Publish the checksum first. This prevents an encrypted artifact without a checksum.
if ! ln "$checksum_path" "$checksum" 2>/dev/null; then
	echo "Backup checksum already exists" >&2
	exit 1
fi
checksum_published=true

if ! ln "$encrypted_path" "$output" 2>/dev/null; then
	# Remove only the checksum link created by this run.
	if [ "$checksum_published" = true ]; then
		rm -f -- "$checksum"
		checksum_published=false
	fi
	echo "Encrypted backup already exists" >&2
	exit 1
fi

printf 'Backup: %s\nChecksum: %s\n' "$output" "$checksum"
