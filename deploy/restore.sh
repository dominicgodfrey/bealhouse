#!/usr/bin/env bash
#
# The other half of backup.sh, in three modes.
#
#     restore.sh verify                 # check the live pair, change nothing
#     restore.sh drill  /var/backups/bealhouse/20260807T040000Z
#     restore.sh restore /var/backups/bealhouse/20260807T040000Z --yes
#
# `verify` and `drill` are the ones to run regularly. `restore` is the one for
# the bad morning, and it will not run without --yes.
#
# What all three actually check is the thing a dump alone cannot tell you:
# every photograph the database points at is a file that exists. The paths are
# in Postgres and the bytes are in MEDIA_DIR, so a backup scheme can be
# perfectly healthy on both halves separately and still restore to a site of
# broken images with nothing in any log about it (decision #16).

set -euo pipefail

MEDIA_DIR="${MEDIA_DIR:-/var/lib/bealhouse/media}"
MEDIA_URL_PREFIX="/media/" # media.URLPrefix; one constant, two languages

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
die() { echo "$*" >&2; exit 1; }

# integrity reports whether every stored photo path resolves to a file.
#
# Both directions are worth knowing and only one of them is a fault. A path with
# no file is a broken picture on a public page. A file with no path is an
# orphan, which is expected and harmless — removing a photo from a room
# deliberately does not delete the bytes, because two rooms may share them.
integrity() {
	local url="$1" media="$2" missing=0 total=0

	while IFS= read -r path; do
		[ -z "$path" ] && continue
		total=$((total + 1))
		local file="$media/${path#"$MEDIA_URL_PREFIX"}"
		if [ ! -f "$file" ]; then
			echo "  MISSING $path"
			missing=$((missing + 1))
		fi
	done < <(psql "$url" -tAc \
		"SELECT path FROM room_photos UNION SELECT path FROM event_photos")

	local orphans
	orphans=$(( $(find "$media" -type f 2>/dev/null | wc -l) - (total - missing) ))

	echo "  $total photographs referenced, $missing missing, ${orphans#-} unreferenced file(s) on disk"
	if [ "$missing" -gt 0 ]; then
		die "$missing photograph(s) have a row and no file. This is what a restore that brought back the database and not MEDIA_DIR looks like."
	fi
	say "Integrity OK"
}

case "${1:-}" in
verify)
	DATABASE_URL="${DATABASE_URL:?set DATABASE_URL, or source /etc/bealhouse/env}"
	say "Checking the live database against $MEDIA_DIR"
	integrity "$DATABASE_URL" "$MEDIA_DIR"
	;;

drill)
	# The tested restore decision #2 asks for, run without touching anything
	# live: the dump goes into a scratch database and the archive into a scratch
	# directory, the pair is checked, and both are thrown away.
	#
	# This is what the weekly `backup.verify` job should shell out to. A backup
	# nobody has restored is a hypothesis, and the useful time to disprove it is
	# not the morning the disk fails.
	set_dir="${2:?usage: restore.sh drill <set-dir>}"
	[ -f "$set_dir/db.dump" ] || die "no db.dump in $set_dir"
	[ -f "$set_dir/media.tar.gz" ] || die "no media.tar.gz in $set_dir"

	DATABASE_URL="${DATABASE_URL:?set DATABASE_URL, or source /etc/bealhouse/env}"
	scratch_db="bealhouse_drill_$(date -u +%s)"
	scratch_media="$(mktemp -d)"
	admin_url="${DATABASE_URL%%\?*}"
	admin_url="${admin_url%/*}/postgres"

	cleanup() {
		psql "$admin_url" -q -c "DROP DATABASE IF EXISTS $scratch_db" >/dev/null 2>&1 || true
		rm -rf "$scratch_media"
	}
	trap cleanup EXIT

	say "Restoring $set_dir into $scratch_db"
	psql "$admin_url" -q -c "CREATE DATABASE $scratch_db"
	# --no-owner because the drill database is created by whoever is running
	# this, not by the role the dump was taken as. Errors are not suppressed:
	# a restore that reported problems is the finding, not the noise.
	pg_restore --no-owner --no-privileges --exit-on-error \
		--dbname "${DATABASE_URL%/*}/$scratch_db" "$set_dir/db.dump"

	tar --extract --gzip --file="$set_dir/media.tar.gz" --directory="$scratch_media"

	say "Checking the restored pair"
	integrity "${DATABASE_URL%/*}/$scratch_db" "$scratch_media"

	# A restore that comes back empty passes every check above trivially, so
	# assert the inn is actually in there.
	rooms=$(psql "${DATABASE_URL%/*}/$scratch_db" -tAc 'SELECT count(*) FROM rooms')
	bookings=$(psql "${DATABASE_URL%/*}/$scratch_db" -tAc 'SELECT count(*) FROM bookings')
	echo "  $rooms rooms, $bookings bookings restored"
	[ "$rooms" -gt 0 ] || die "the restored database has no rooms in it"

	say "Drill passed; scratch database and files removed"
	;;

restore)
	set_dir="${2:?usage: restore.sh restore <set-dir> --yes}"
	[ "${3:-}" = "--yes" ] || die "this replaces the live database and MEDIA_DIR. Re-run with --yes if that is what you want."
	[ -f "$set_dir/db.dump" ] || die "no db.dump in $set_dir"
	[ -f "$set_dir/media.tar.gz" ] || die "no media.tar.gz in $set_dir"

	DATABASE_URL="${DATABASE_URL:?set DATABASE_URL, or source /etc/bealhouse/env}"

	say "Stopping the service so nothing writes during the restore"
	systemctl stop bealhouse || true

	say "Restoring the database"
	# --clean --if-exists drops what is there first. Without it a restore into a
	# database that still has rows merges the two, and the result is a booking
	# ledger nobody can reason about.
	pg_restore --clean --if-exists --no-owner --no-privileges \
		--dbname "$DATABASE_URL" "$set_dir/db.dump"

	say "Restoring $MEDIA_DIR"
	mkdir -p "$MEDIA_DIR"
	tar --extract --gzip --file="$set_dir/media.tar.gz" --directory="$MEDIA_DIR"

	say "Checking the restored pair"
	integrity "$DATABASE_URL" "$MEDIA_DIR"

	say "Starting the service"
	systemctl start bealhouse
	;;

*)
	cat >&2 <<'EOF'
usage:
  restore.sh verify                    check the live database against MEDIA_DIR
  restore.sh drill   <set-dir>         restore into a scratch database and check it
  restore.sh restore <set-dir> --yes   replace the live database and MEDIA_DIR
EOF
	exit 2
	;;
esac
