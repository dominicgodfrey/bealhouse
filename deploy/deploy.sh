#!/usr/bin/env bash
#
# Build here, ship one file, migrate, swap, restart, verify — and put the old
# binary back if the new one does not come up.
#
#     BEAL_HOST=inn@203.0.113.10 ./deploy/deploy.sh
#
# Runs from a developer's machine (Git Bash on Windows is fine) or from CI. It
# needs ssh and scp, and nothing on the server beyond systemd and the unit.
#
# The order below is the whole design:
#
#   1. build the SPA, then the binary — the bundle is embedded, so the reverse
#      order ships yesterday's front end inside today's server and nothing says
#      so
#   2. copy the new binary to a scratch path
#   3. run migrations *with the new binary*, before it is installed — a
#      migration that fails leaves the old one still serving guests
#   4. swap atomically and restart
#   5. check health, and roll back to the previous binary if it does not answer
#
# Step 3 means the old binary briefly runs against the new schema. That window
# is a couple of seconds, but it is the reason a migration should be additive:
# add a column and backfill in one deploy, start requiring it in the next.

set -euo pipefail

: "${BEAL_HOST:?set BEAL_HOST, e.g. inn@203.0.113.10}"
BEAL_SSH_OPTS="${BEAL_SSH_OPTS:-}"
REMOTE_BIN="${REMOTE_BIN:-/usr/local/bin/bealhouse}"
SERVICE="${SERVICE:-bealhouse}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/api/health}"

cd "$(dirname "$0")/.."

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
# shellcheck disable=SC2086
remote() { ssh $BEAL_SSH_OPTS "$BEAL_HOST" "$@"; }

if [ -n "$(git status --porcelain)" ]; then
	say "WARNING: the working tree is dirty; deploying it anyway"
	git status --short
fi
REVISION="$(git rev-parse --short HEAD)"

say "Building the SPA"
(cd web && npm ci --no-fund --no-audit && npm run build)

say "Building the binary for linux/amd64"
# CGO off so it is a static binary that does not care what libc the VPS has.
# This project has no cgo anyway — which is also why `go test -race` does not
# work on the development machine.
rm -f bin/bealhouse-linux
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build -trimpath -ldflags "-s -w" -o bin/bealhouse-linux ./cmd/server

# The stale-binary trap, closed. A build that silently did not happen ships the
# last one that did, and the symptom is a route added an hour ago answering 404
# while everything else looks fine.
if ! head -c 4 bin/bealhouse-linux | grep -q ELF; then
	echo "bin/bealhouse-linux is not a Linux binary; the cross-compile did not happen" >&2
	exit 1
fi
say "Built $(du -h bin/bealhouse-linux | cut -f1) from $REVISION"

say "Copying to $BEAL_HOST"
# shellcheck disable=SC2086
scp $BEAL_SSH_OPTS bin/bealhouse-linux "$BEAL_HOST:/tmp/bealhouse.new"

say "Migrating with the new binary, before installing it"
remote "sudo install -m 0755 -o root -g root /tmp/bealhouse.new ${REMOTE_BIN}.new \
	&& sudo systemd-run --pipe --wait --collect \
		--property=User=bealhouse \
		--property=EnvironmentFile=/etc/bealhouse/env \
		${REMOTE_BIN}.new migrate up"

say "Installing and restarting"
# The previous binary is kept, not overwritten: it is the rollback, and the
# alternative is re-running this whole script from whatever the last good commit
# turns out to have been, at the point where the site is already down.
remote "sudo cp -a ${REMOTE_BIN} ${REMOTE_BIN}.prev 2>/dev/null || true; \
	sudo mv ${REMOTE_BIN}.new ${REMOTE_BIN}; \
	sudo systemctl restart ${SERVICE}"

say "Checking health"
# /api/health answers 200 even when the database is unreachable — it reports the
# state rather than failing — so this reads the field and not the status code. A
# deploy that "passed" while every page 503s is worse than one that failed.
healthy=false
for _ in $(seq 1 20); do
	if remote "curl -fsS --max-time 3 ${HEALTH_URL}" 2>/dev/null | grep -q '"db":"up"'; then
		healthy=true
		break
	fi
	sleep 1
done

if [ "$healthy" != true ]; then
	say "FAILED to come up; rolling back"
	remote "if [ -f ${REMOTE_BIN}.prev ]; then \
			sudo mv ${REMOTE_BIN}.prev ${REMOTE_BIN} && sudo systemctl restart ${SERVICE}; \
		fi; \
		sudo systemctl status ${SERVICE} --no-pager --lines 40 || true"
	echo
	echo "Rolled back to the previous binary. The database has already been migrated," >&2
	echo "so the schema is ahead of the code now running — check the unit's log before" >&2
	echo "deploying again." >&2
	exit 1
fi

say "Live: $REVISION"
