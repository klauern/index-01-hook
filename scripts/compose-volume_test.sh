#!/bin/sh
set -eu

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

if ! command -v docker >/dev/null 2>&1; then
	fail "Docker is required"
fi

test_root=$(mktemp -d "${TMPDIR:-/tmp}/compose-volume-test.XXXXXX")
volume_name=
legacy_volume_name=
create_container_name=
backup_container_name=
restore_container_name=
verify_container_name=
legacy_create_container_name=
legacy_verify_container_name=

cleanup() {
	status=$?
	trap - EXIT HUP INT TERM
	if command -v docker >/dev/null 2>&1; then
		for container_name in \
			"${create_container_name:-}" \
			"${backup_container_name:-}" \
			"${restore_container_name:-}" \
			"${verify_container_name:-}" \
			"${legacy_create_container_name:-}" \
			"${legacy_verify_container_name:-}"; do
			if [ -n "$container_name" ]; then
				docker rm -f "$container_name" >/dev/null 2>&1 || :
			fi
		done
		if [ -n "${volume_name:-}" ]; then
			docker volume rm "$volume_name" >/dev/null 2>&1 || :
		fi
		if [ -n "${legacy_volume_name:-}" ]; then
			docker volume rm "$legacy_volume_name" >/dev/null 2>&1 || :
		fi
	fi
	rm -rf "$test_root"
	exit "$status"
}
trap cleanup EXIT HUP INT TERM

if ! docker info >/dev/null 2>"$test_root/docker-info.stderr"; then
	fail "Docker daemon is unavailable"
fi

image=${INDEX01_TEST_IMAGE:-index-01-hook:local}
configured_user=$(docker image inspect --format '{{.Config.User}}' "$image" 2>/dev/null) ||
	fail "Docker image is unavailable"
[ "$configured_user" = "65532:65532" ] ||
	fail "Docker image does not configure user 65532:65532"

suffix=${test_root##*/}
volume_name=index-01-hook-volume-test-$suffix
create_container_name=index-01-hook-create-test-$suffix
backup_container_name=index-01-hook-backup-test-$suffix
restore_container_name=index-01-hook-restore-test-$suffix
verify_container_name=index-01-hook-verify-test-$suffix
legacy_volume_name=index-01-hook-legacy-volume-test-$suffix
legacy_create_container_name=index-01-hook-legacy-create-test-$suffix
legacy_verify_container_name=index-01-hook-legacy-verify-test-$suffix
database_path=/var/lib/index-01-hook/data/index01.db
backup_path=$test_root/synthetic-backup.db
volume_backup_path=/var/lib/index-01-hook/data/synthetic-backup.db

if docker volume inspect "$volume_name" >/dev/null 2>&1; then
	fail "Docker test volume name was not unique"
fi
if ! docker volume create "$volume_name" >/dev/null 2>"$test_root/volume.stderr"; then
	fail "Docker could not create the fresh named volume"
fi

# Create a valid synthetic SQLite database before exporting the backup.
if ! docker run --rm --network none --cap-drop ALL --security-opt no-new-privileges:true --name "$create_container_name" \
	--volume "$volume_name:/var/lib/index-01-hook" \
	--env "INDEX01_DB_PATH=$database_path" \
	--env INDEX01_PURGE_CONFIRM=purge-expired-recordings \
	"$image" purge-expired >"$test_root/create.stdout" 2>"$test_root/create.stderr"; then
	fail "operator database creation failed"
fi
grep -F '"state":"purged"' "$test_root/create.stdout" >/dev/null ||
	fail "operator database creation returned an unexpected result"

if ! docker run --rm --network none --cap-drop ALL --security-opt no-new-privileges:true --name "$backup_container_name" \
	--volume "$volume_name:/var/lib/index-01-hook" \
	--env "INDEX01_DB_PATH=$database_path" \
	"$image" backup - >"$backup_path" 2>"$test_root/backup.stderr"; then
	fail "operator database backup failed"
fi
[ -s "$backup_path" ] || fail "operator database backup was empty"

if ! docker run --rm --network none --cap-drop ALL --security-opt no-new-privileges:true --name "$restore_container_name" \
	--volume "$volume_name:/var/lib/index-01-hook" \
	--volume "$backup_path:$volume_backup_path:ro" \
	--env "INDEX01_DB_PATH=$database_path" \
	"$image" restore "$volume_backup_path" >"$test_root/restore.stdout" 2>"$test_root/restore.stderr"; then
	fail "operator database restore failed"
fi
[ "$(cat "$test_root/restore.stdout")" = '{"state":"restored"}' ] ||
	fail "operator database restore returned an unexpected result"

# A second backup proves that the restored database is usable at the data path.
if ! docker run --rm --network none --cap-drop ALL --security-opt no-new-privileges:true --name "$verify_container_name" \
	--volume "$volume_name:/var/lib/index-01-hook" \
	--env "INDEX01_DB_PATH=$database_path" \
	"$image" backup - >"$test_root/verify-backup.db" 2>"$test_root/verify.stderr"; then
	fail "restored database could not be opened"
fi
[ -s "$test_root/verify-backup.db" ] || fail "restored database backup was empty"

# A root-owned legacy data directory must not become writable through the non-root application.
if docker volume inspect "$legacy_volume_name" >/dev/null 2>&1; then
	fail "legacy Docker test volume name was not unique"
fi
if ! docker volume create "$legacy_volume_name" >/dev/null 2>"$test_root/legacy-volume.stderr"; then
	fail "Docker could not create the legacy test volume"
fi
if ! docker run --rm --network none --cap-drop ALL --security-opt no-new-privileges:true --name "$legacy_create_container_name" --user 0:0 \
	--mount "type=volume,source=$legacy_volume_name,target=/var/lib/index-01-hook,volume-nocopy" \
	--env "INDEX01_DB_PATH=$database_path" \
	--env INDEX01_PURGE_CONFIRM=purge-expired-recordings \
	"$image" purge-expired >"$test_root/legacy-create.stdout" 2>"$test_root/legacy-create.stderr"; then
	fail "root-owned legacy directory setup failed"
fi
if docker run --rm --network none --cap-drop ALL --security-opt no-new-privileges:true --name "$legacy_verify_container_name" \
	--mount "type=volume,source=$legacy_volume_name,target=/var/lib/index-01-hook,volume-nocopy" \
	--env "INDEX01_DB_PATH=$database_path" \
	--env INDEX01_PURGE_CONFIRM=purge-expired-recordings \
	"$image" purge-expired >"$test_root/legacy-verify.stdout" 2>"$test_root/legacy-verify.stderr"; then
	fail "non-root application accepted an incompatible root-owned legacy directory"
fi

echo "PASS: fresh named volume is writable by the configured non-root user and supports SQLite restore"
