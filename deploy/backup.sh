#!/usr/bin/env bash
#
# The nightly backup: the database *and* the photographs, as one set.
#
#     sudo -u bealhouse /usr/local/share/bealhouse/backup.sh
#
# Run from deploy/bealhouse-backup.timer. See restore.sh for the other half —
# and run it, because a backup nobody has restored is a hypothesis.
#
# Two things travel together here and the pairing is the whole point.
# `pg_dump` does not contain MEDIA_DIR: the photographs are files on disk and
# the database holds only their paths. A restore that brings back the paths and
# not the files is a site of broken images with nothing in any log to say so
# (decision #16). So both are written under one timestamp, and restore.sh
# refuses a pair that does not match.

set -euo pipefail

DATABASE_URL="${DATABASE_URL:?set DATABASE_URL, or source /etc/bealhouse/env}"
MEDIA_DIR="${MEDIA_DIR:-/var/lib/bealhouse/media}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/bealhouse}"
KEEP_DAYS="${KEEP_DAYS:-14}"

# Where the copy that survives the server going away lives (decision #2: a
# provider snapshot is not a tested restore). Anything rclone can address:
#
#     BACKUP_REMOTE=b2:bealhouse-backups
#
# Left empty, the backup is local only — which is a real backup of a disk
# failure and no backup at all of the machine.
BACKUP_REMOTE="${BACKUP_REMOTE:-}"

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
set="$BACKUP_DIR/$stamp"
mkdir -p "$set"

say() { printf '==> %s\n' "$*"; }

# -Fc is the custom format: compressed, and restorable selectively by
# pg_restore, which matters on the day somebody needs one table back rather
# than the whole inn.
say "Dumping the database"
pg_dump --format=custom --no-owner --no-privileges --file="$set/db.dump" "$DATABASE_URL"

say "Archiving $MEDIA_DIR"
if [ -d "$MEDIA_DIR" ]; then
	# A full archive each night rather than anything incremental. The files are
	# content-addressed and never change, so an incremental scheme would be
	# strictly cleverer and its failure mode is a restore that needs several
	# nights' archives to be complete — for an inn with seven rooms' worth of
	# photographs, that is a worse trade than the disk space.
	tar --create --gzip --file="$set/media.tar.gz" --directory="$MEDIA_DIR" .
else
	say "WARNING: $MEDIA_DIR does not exist; archiving nothing"
	tar --create --gzip --file="$set/media.tar.gz" --files-from=/dev/null
fi

# The manifest is what restore.sh checks the pair against, and what makes a
# half-written set — the machine rebooting between the two commands above —
# visibly incomplete rather than quietly short a photograph.
cat >"$set/manifest.txt" <<EOF
taken_at=$stamp
database=$(psql "$DATABASE_URL" -tAc 'SELECT current_database()')
media_dir=$MEDIA_DIR
media_files=$(find "$MEDIA_DIR" -type f 2>/dev/null | wc -l)
photo_rows=$(psql "$DATABASE_URL" -tAc 'SELECT count(*) FROM room_photos')
event_photo_rows=$(psql "$DATABASE_URL" -tAc 'SELECT count(*) FROM event_photos')
db_bytes=$(stat -c %s "$set/db.dump")
media_bytes=$(stat -c %s "$set/media.tar.gz")
EOF

# An empty dump is the failure this catches: pg_dump exiting 0 having written
# nothing useful, which then quietly replaces last night's good one in the
# retention window below.
if [ "$(stat -c %s "$set/db.dump")" -lt 4096 ]; then
	echo "the dump is $(stat -c %s "$set/db.dump") bytes; refusing to call that a backup" >&2
	exit 1
fi

if [ -n "$BACKUP_REMOTE" ]; then
	say "Uploading to $BACKUP_REMOTE"
	rclone copy "$set" "$BACKUP_REMOTE/$stamp" --transfers 2
fi

say "Pruning local sets older than $KEEP_DAYS days"
find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -mtime "+$KEEP_DAYS" -exec rm -rf {} +

say "Done: $set"
cat "$set/manifest.txt"
