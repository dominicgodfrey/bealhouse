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
| `bealhouse-verify.{service,timer}` | `/etc/systemd/system/` | Sunday 05:30, the restore drill |

---

## Provisioning, once

**Debian 12.** Ubuntu 24.04 works the same way; substitute `noble` for
`bookworm` in the PostgreSQL source below. Everything here is root, over ssh,
and the box needs no domain yet — nothing until the Caddy step cares.

If the provider's console does not offer the CX line in Ashburn, take the **AMD
equivalent** at the same memory rather than dropping a size — decision #2 rests
on the 4 GB, not on the processor, because the image pipeline is what a smaller
box fails on.

**Attach the ssh key when the server is created**, in the provider's console
rather than afterwards. That writes it to root's `authorized_keys` on first boot
and leaves password authentication off; adding it later means a window with a
root password answering on the public internet.

### The clock

`internal/civil` resolves in America/New_York and the backup timer fires on
local time, so a box left on UTC dumps at the wrong hour and disagrees with the
inn about which day it is.

```bash
timedatectl set-timezone America/New_York
```

### Base packages, and security updates

```bash
apt update && apt -y full-upgrade && apt install -y curl ca-certificates gnupg unattended-upgrades
```

Unattended upgrades are safe here because the service was written for them:
`bealhouse.service` says `Wants=postgresql.service` and not `Requires=`, and the
binary is built to survive a database it cannot reach. A Postgres restart for a
security patch is a couple of seconds of `"db":"down"` on `/api/health`, not an
outage — which is the same property the health endpoint exists to report.

### Swap

The CX22 ships with none, and 4 GB with an image pipeline decoding 4000px phone
photographs is where the OOM killer picks something. On this box the something
it picks would be Postgres.

```bash
fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile && echo '/swapfile none swap sw 0 0' >> /etc/fstab
```

### Firewall

Three ports in. Postgres binds to loopback by default and must stay there —
`DATABASE_URL` carries `sslmode=disable`, which is only defensible while the
connection never leaves the machine.

```bash
apt install -y ufw && ufw allow 22/tcp && ufw allow 80/tcp && ufw allow 443/tcp && ufw --force enable
```

### PostgreSQL 17, from PGDG

**Not `apt install postgresql`,** which on Debian 12 is PostgreSQL 15 — two
majors behind what this project is developed and tested against.
`docker-compose.yml` and both CI jobs pin `postgres:17-alpine`, and the whole
architecture is a bet on Postgres behaviour: the exclusion constraint,
`pg_advisory_xact_lock`, range types, `now()` meaning the transaction's start,
`SKIP LOCKED` in the jobs runner. All of those are stable well before 15, so 15
would very probably be fine — but "probably fine" is a strange thing to say
about the layer that prevents double-booking, and closing the gap costs one apt
source.

```bash
install -d /usr/share/postgresql-common/pgdg && curl -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc --fail https://www.postgresql.org/media/keys/ACCC4CF8.asc
```

```bash
echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" > /etc/apt/sources.list.d/pgdg.list && apt update && apt install -y postgresql-17
```

### Caddy, from Caddy

**Not `apt install caddy` either.** Debian 12 does carry a `caddy` package and
it is **2.6.2**, from late 2022. For the process terminating TLS on the public
internet, take the current stable from the repository Caddy itself documents.

```bash
apt install -y debian-keyring debian-archive-keyring apt-transport-https && curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg && curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list && apt update && apt install -y caddy
```

The package starts Caddy immediately, serving its own default page on port 80
until the Caddyfile below replaces it. That page is a useful sign the box is
reachable before there is anything else to ask for.

### The service account, the database, the directories

**Two different things are called `bealhouse` here and they are unrelated**: a
Unix system account the binary runs as, and a Postgres role. Same name on
purpose, no connection between them.

```bash
adduser --system --group --home /var/lib/bealhouse bealhouse
```

```bash
sudo -u postgres createuser bealhouse
sudo -u postgres createdb --owner bealhouse bealhouse
sudo -u postgres psql -c "ALTER USER bealhouse WITH PASSWORD '<a long random one>'"
```

Keep that password; it goes into `DATABASE_URL` below.

`btree_gist` is what the exclusion constraint that prevents double-booking is
built on. The first migration creates the extension and needs a superuser to do
it, which is why it is here and not in the migration path:

```bash
sudo -u postgres psql -d bealhouse -c 'CREATE EXTENSION IF NOT EXISTS btree_gist'
```

```bash
install -d -o bealhouse -g bealhouse /var/lib/bealhouse/media
install -d -o bealhouse -g bealhouse /var/backups/bealhouse
install -d -o root -g root -m 0755 /usr/local/share/bealhouse
install -d -o root -g root -m 0750 /etc/bealhouse
```

### The deploy account

`deploy.sh` connects as `$BEAL_HOST` and runs `sudo` over a **non-interactive**
ssh, so the account it uses needs passwordless sudo — a password prompt there is
a hang with no output and nothing in any log.

```bash
adduser --disabled-password --gecos "" inn
install -d -m 700 -o inn -g inn /home/inn/.ssh
cp /root/.ssh/authorized_keys /home/inn/.ssh/ && chown inn:inn /home/inn/.ssh/authorized_keys
```

```bash
echo 'inn ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/inn && chmod 440 /etc/sudoers.d/inn && visudo -c
```

Deploying as `root@` instead works and skips this entirely. The trade is that
every deploy is then a root session, where this way the account with passwordless
sudo is one that exists only to deploy.

### Before moving on

```bash
psql -h 127.0.0.1 -U bealhouse -d bealhouse -c 'SELECT version()'
ss -ltnp | grep 5432
```

PostgreSQL 17.x, and 5432 bound to `127.0.0.1` — never `0.0.0.0`. It is the
default and it is worth seeing rather than assuming, for the `sslmode=disable`
reason above.

```bash
ssh inn@<the box> 'sudo systemctl is-active caddy && timedatectl | head -3'
```

`active`, `America/New_York`, and no prompt for anything.

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

### The Caddyfile

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
cp deploy/bealhouse.service \
   deploy/bealhouse-backup.service deploy/bealhouse-backup.timer \
   deploy/bealhouse-verify.service deploy/bealhouse-verify.timer \
   /etc/systemd/system/
install -m 0755 deploy/backup.sh deploy/restore.sh /usr/local/share/bealhouse/
systemctl daemon-reload
systemctl enable --now bealhouse bealhouse-backup.timer bealhouse-verify.timer
```

### The seed, once

Reference data the owner then edits. **Not** run by `deploy.sh`: re-running it
over a live database is not something a deploy should be able to do by accident.
All four files are re-runnable.

**The order is not decoration and neither is what is missing from it.**
`rooms.sql` describes the seven rooms as facts — occupancy, beds, views, the pet
room — and deliberately leaves every description as the literal string
`PLACEHOLDER`, so that one reaching the live site is unmistakable rather than
plausible. `content.sql` is what clears them: the owner's own sentences,
transcribed from the inn's current site, and with them the amenities and the
prose on six pages. **Run rooms and stop, and seven room pages say
"PLACEHOLDER — final copy to be supplied by the owner." to the public internet.**

```bash
cd /path/to/checkout
sudo -u postgres psql -d bealhouse -v ON_ERROR_STOP=1 -f internal/db/seed/rooms.sql
sudo -u postgres psql -d bealhouse -v ON_ERROR_STOP=1 -f internal/db/seed/content.sql
sudo -u postgres psql -d bealhouse -v ON_ERROR_STOP=1 -f internal/db/seed/attractions.sql
sudo -u postgres psql -d bealhouse -v ON_ERROR_STOP=1 -f internal/db/seed/rates.sql
```

Then check that none of it stayed behind, which is one query and is worth the
ten seconds:

```bash
sudo -u postgres psql -d bealhouse -c "SELECT slug FROM rooms WHERE description LIKE 'PLACEHOLDER%'"
```

**`internal/db/seed/menu-mock.sql` is not in that list and must not be run on
this box.** It is invented food, written to exercise the menu editor, and it is
the only seeded content that is nobody's real words. A restaurant page with no
menu correctly says the menu is not up and to ring the inn; a restaurant page
with five invented dishes on it is a lie that stays up until somebody remembers.

`rates.sql` is the one seed whose numbers charge a card — a single flat
placeholder season, so the rooms are sellable on day one. Replacing it is the
first item in [OWNER-SETUP.md](../OWNER-SETUP.md), and the only item there that
can charge somebody the wrong amount.

### The first phone

There is no password anywhere (decision #15), and `bealhouse enroll` proving
shell access to this server is the only way in when no phone is enrolled. Every
enrollment after the first can be minted from an already-signed-in console.

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

`rclone` is not in the provisioning list above, because until a destination is
chosen there is nothing for it to talk to:

```bash
apt install -y rclone
sudo -u bealhouse rclone config      # writes ~bealhouse/.config/rclone
```

Do both, or neither. `backup.sh` runs under `set -e`, so a `BACKUP_REMOTE` set
with no `rclone` installed makes the nightly unit fail *after* it has written a
perfectly good local set — which reads like a broken backup and is really a
missing package.

**The drill runs itself**, from `bealhouse-verify.timer` every Sunday at 05:30 —
after Sunday's backup has finished, on the set it just wrote. It restores into a
scratch database and a temporary directory, checks every photo row against a
real file, and throws both away, so it touches nothing live.

It is a **systemd timer and not a job on the application's runner**, which is
where an earlier draft of ARCHITECTURE.md put it, for three reasons. The drill
needs `CREATE DATABASE`, `pg_restore` and a writable scratch directory, and
granting those to the process serving the public internet is strictly worse than
a oneshot unit that has them for four minutes a week. It runs for minutes, where
the jobs runner exists for work that belongs to a transaction. And the backup it
proves is already a timer, so a drill on a different mechanism is the surprising
arrangement rather than the consistent one.

```bash
# Did last Sunday's drill pass?
systemctl status bealhouse-verify.service --no-pager
systemctl list-timers bealhouse-\* --no-pager

# Run one now, against the newest set. This is exactly what the timer runs.
/usr/local/share/bealhouse/restore.sh drill

# Or against a particular set.
/usr/local/share/bealhouse/restore.sh drill /var/backups/bealhouse/20260807T040000Z

# The same integrity check against what is live right now.
/usr/local/share/bealhouse/restore.sh verify

# The bad morning. Stops the service, replaces the database and MEDIA_DIR,
# re-checks, starts it again. Refuses without --yes.
/usr/local/share/bealhouse/restore.sh restore /var/backups/bealhouse/20260807T040000Z --yes
```

**A failed drill is a failed unit and nothing else** — it is in the journal and
in `systemctl list-units --failed`, and nobody is told. That is the honest state
of it: the drill answers the question, and reading the answer is still a person's
job until something watches the box from outside (see below). It is a far better
place to be than a backup nobody has ever restored, and it is not the same as
being alerted.

---

## Still to do at launch

- **Sentry** — **the wiring is built**; what is left is a project and its DSN in
  `/etc/bealhouse/env` as `SENTRY_DSN`. It is an `slog` handler, so it reports
  what is already reported and there was no audit of call sites; `ENV` becomes
  the Sentry environment, and only Error and above are sent, because WARN here
  is how the binary says "no Stripe key". Empty means the journal on this box is
  the only copy.
- **Uptime monitoring** — something outside this box asking for
  `/api/health`. Note that it answers **200 with `"db":"down"`** rather than
  failing, so the check has to read the field; `deploy.sh` does the same. This
  is also what would turn a failed restore drill from a line in the journal into
  something somebody hears about.
- **DNS cutover**, Search Console (`/sitemap.xml` is live and generated), and
  the Google Business Profile.
- **Stripe live keys**, after the verification matrix in ARCHITECTURE.md.
- **Resend DNS** — SPF, DKIM and DMARC at Bluehost (decision #17); SPF has to
  include Resend *and* the mailbox host.
- **An off-box copy of the backups.** The nightly set and the weekly drill both
  live on the same disk as the database they protect, which covers a bad
  migration and a deleted row and does not cover losing the box. That needs a
  destination somebody has to choose and pay for — see `BACKUP_REMOTE` above.
