# Deploying Beal House

One binary, one Postgres, Caddy in front (decision #2: Hetzner CX22, Ashburn).
Nothing here is containerised — `docker-compose.yml` is the local database only.

Files in this directory:

| File | Goes to | What it is |
|---|---|---|
| `bealhouse.service` | `/etc/systemd/system/` | the binary as a service, hardened |
| `Caddyfile` | `/etc/caddy/` | TLS, the www redirect, the reverse proxy |
| `deploy.sh` | stays here | build → ship → migrate → swap → verify → roll back |
| `backup.sh` | `/usr/local/share/bealhouse/` | nightly dump **and** photographs |
| `restore.sh` | `/usr/local/share/bealhouse/` | verify, drill, restore |
| `bealhouse-backup.{service,timer}` | `/etc/systemd/system/` | 04:00 nightly |

---

## Provisioning, once

Debian 12 or Ubuntu 24.04. Everything below is root.

```bash
# The clock. internal/civil resolves in America/New_York and the backup timer
# fires on local time, so the box agrees with the inn.
timedatectl set-timezone America/New_York

apt update && apt install -y postgresql caddy curl
adduser --system --group --home /var/lib/bealhouse bealhouse

sudo -u postgres createuser bealhouse
sudo -u postgres createdb --owner bealhouse bealhouse
sudo -u postgres psql -c "ALTER USER bealhouse WITH PASSWORD '<a long random one>'"

# btree_gist, for the exclusion constraint that prevents double-booking. The
# first migration creates the extension and needs a superuser to do it.
sudo -u postgres psql -d bealhouse -c 'CREATE EXTENSION IF NOT EXISTS btree_gist'

install -d -o bealhouse -g bealhouse /var/lib/bealhouse/media
install -d -o bealhouse -g bealhouse /var/backups/bealhouse
install -d -o root -g root -m 0755 /usr/local/share/bealhouse
install -d -o root -g root -m 0750 /etc/bealhouse
```

### `/etc/bealhouse/env`

Root-owned, `chmod 0600`. systemd reads it before dropping privileges, so the
service user never needs to. Everything in `.env.example` applies; these are the
lines that are specific to being deployed:

```ini
ADDR=127.0.0.1:8080
ENV=production

# Not optional behind Caddy. Caddy appends to X-Forwarded-For rather than
# replacing it, and the app only believes the header when this says a proxy
# exists. Unset, every guest shares one rate-limit bucket and the booking limit
# looks like a mysterious 429 under normal traffic.
BEHIND_PROXY=true

DATABASE_URL=postgres://bealhouse:<password>@127.0.0.1:5432/bealhouse?sslmode=disable
SITE_URL=https://bealhouse.com

# In neither the binary nor pg_dump. Under /var/lib so a deploy does not
# overwrite it and backup.sh does reach it (decision #16).
MEDIA_DIR=/var/lib/bealhouse/media

# No default and it must not get one: generated at boot, every outstanding
# manage-booking link dies on each restart; compiled in, anyone with the source
# can cancel any guest's stay. `openssl rand -base64 32`. Treat as permanent.
BOOKING_LINK_SECRET=

# Not yet — see ARCHITECTURE.md. Until both Stripe keys are set the endpoints
# that move money refuse and everything else works.
STRIPE_SECRET_KEY=
STRIPE_WEBHOOK_SECRET=
STRIPE_PUBLISHABLE_KEY=
RESEND_API_KEY=
EMAIL_FROM=
OWNER_EMAIL=
```

`STRIPE_FAKE` must never appear in this file. The binary refuses to start with it
set unless no Stripe variable is configured *and* `ENV` is dev, but the reason it
is checked at all is that `ENV` defaults to dev — an unconfigured production
deploy would otherwise look exactly like a laptop.

### Caddy

```bash
cp deploy/Caddyfile /etc/caddy/Caddyfile
printf 'BEAL_DOMAIN=bealhouse.com\nBEAL_ADMIN_EMAIL=owner@bealhouse.com\n' > /etc/default/caddy
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy
```

The domain is the owner's, so it is read from the environment rather than
committed here.

### The units

```bash
cp deploy/bealhouse.service deploy/bealhouse-backup.service deploy/bealhouse-backup.timer \
   /etc/systemd/system/
install -m 0755 deploy/backup.sh deploy/restore.sh /usr/local/share/bealhouse/
systemctl daemon-reload
systemctl enable --now bealhouse bealhouse-backup.timer
```

### The seed, once

The seven rooms and the placeholder rate season. **Not** run by `deploy.sh`: it
is reference data the owner then edits, and re-running it over a live database
is not something a deploy should be able to do by accident.

```bash
sudo -u postgres psql -d bealhouse -v ON_ERROR_STOP=1 -f internal/db/seed/rooms.sql
sudo -u postgres psql -d bealhouse -v ON_ERROR_STOP=1 -f internal/db/seed/rates.sql
```

### The first phone

There is no password anywhere (decision #15), and `bealhouse enroll` proving
shell access to this server is the only way in when no phone is enrolled. Every
enrolment after the first can be minted from an already-signed-in console.

```bash
sudo -u bealhouse env $(cat /etc/bealhouse/env | xargs) /usr/local/bin/bealhouse enroll "Owner's iPhone"
```

The token is printed and never logged, and it travels in the URL **fragment** —
not sent to the server, not in an access log, not in a Referer.

---

## Deploying

From a checkout, on any machine with ssh and the Go and Node toolchains:

```bash
BEAL_HOST=inn@203.0.113.10 ./deploy/deploy.sh
```

It builds the SPA before the binary (the bundle is embedded — the other order
ships yesterday's front end inside today's server and nothing says so),
cross-compiles static for linux/amd64, copies one file, runs `bealhouse migrate
up` **with the new binary before installing it**, swaps atomically, restarts,
and rolls back to the previous binary if health does not come up.

Two consequences of that order worth knowing:

- A failed migration leaves the old binary serving guests, which is the point.
- The old binary runs against the new schema for a second or two. Migrations
  should therefore be additive: add a column and backfill in one deploy, start
  requiring it in the next.

A rollback restores the binary and **not** the database. The script says so when
it happens.

---

## Backups, and actually restoring one

`pg_dump` does not contain `MEDIA_DIR`. The database holds the photographs'
paths and the disk holds their bytes, so a backup scheme can be healthy on both
halves separately and still restore to a site of broken images with nothing in
any log to say so. `backup.sh` writes both under one timestamp and `restore.sh`
checks the pair.

Off-box copies go wherever rclone can address — set `BACKUP_REMOTE` in
`/etc/bealhouse/backup.env`. A provider snapshot is not a substitute and is not
a tested restore.

```bash
# The drill. Restores into a scratch database and a temporary directory, checks
# every photo row against a real file, then throws both away. Touches nothing
# live, so there is no reason not to run it weekly — this is what the
# backup.verify job in ARCHITECTURE.md should shell out to.
/usr/local/share/bealhouse/restore.sh drill /var/backups/bealhouse/20260807T040000Z

# The same integrity check against what is live right now.
/usr/local/share/bealhouse/restore.sh verify

# The bad morning. Stops the service, replaces the database and MEDIA_DIR,
# re-checks, starts it again. Refuses without --yes.
/usr/local/share/bealhouse/restore.sh restore /var/backups/bealhouse/20260807T040000Z --yes
```

---

## Still to do at launch

- **Sentry** — a DSN and the wiring. `slog` is what everything already logs
  through, so it is a handler, not an audit of call sites.
- **Uptime monitoring** — something outside this box asking for
  `/api/health`. Note that it answers **200 with `"db":"down"`** rather than
  failing, so the check has to read the field; `deploy.sh` does the same.
- **DNS cutover**, Search Console (`/sitemap.xml` is live and generated), and
  the Google Business Profile.
- **Stripe live keys**, after the verification matrix in ARCHITECTURE.md.
- **Resend DNS** — SPF, DKIM and DMARC at Bluehost (decision #17); SPF has to
  include Resend *and* the mailbox host.
