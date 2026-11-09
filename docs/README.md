# Configuration and usage

[Quick start](../README.md) · [Watchtower](WATCHTOWER.md) · [Logs](LOGGING.md)

Commands run from the project root. Domains, email addresses, and IDs in
examples are placeholders.

## Configuration

Copy `.env.example` to `.env`. Docker Compose loads it automatically;
local binaries require the variables to be exported into the environment.

| Variable | Purpose |
| --- | --- |
| `ORIGIN` | Public HTTPS origin, e.g. `https://push.example.com`; no trailing slash or path. Also determines the passkey hostname. |
| `VAPID_SUBJECT` | Push-service contact, e.g. `mailto:admin@example.com`. Defaults to `ORIGIN` if empty. |
| `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY` | Matching P-256 key pair. Keep stable across restarts. |
| `SEND_TOKEN` | Server-wide API sending token; at least 32 characters. |
| `ADMIN_TOKEN` | Separate administration API token; at least 32 characters. |
| `BOOTSTRAP_OTP` | One-time first-admin setup secret; 20–256 characters. Clear after registration. |
| `ADDR` | Local listen address; sample uses `127.0.0.1:8099`. Docker listens on `:8099`. |
| `DB_PATH` | Sample uses `shared-data/webpush.sqlite`; Compose overrides it to `/data/webpush.sqlite`. |
| `TRUSTED_PROXIES` | Comma-separated proxy CIDRs; empty by default. Trust only your actual proxies. |

Missing or invalid credentials are generated at startup. The server prints
new assignments and exits until they are saved. Setup output contains secrets.
An empty `BOOTSTRAP_OTP` disables bootstrap when the other credentials are valid.

### Generate credentials manually

With Docker:

```sh
docker compose build
docker compose run --rm --no-deps push /usr/local/bin/pushie genkeys
```

With Go:

```sh
(cd src && go run . genkeys)
```

Both commands print the VAPID assignments. For `SEND_TOKEN`, `ADMIN_TOKEN`,
and `BOOTSTRAP_OTP`, run this command separately for each value:

```sh
openssl rand -hex 32
```

Copy the values into `.env`. All three secrets must differ.

## HTTPS and proxy

Example Caddy configuration for a proxy on the host:

```caddyfile
push.example.com {
    reverse_proxy 127.0.0.1:8099
}
```

Set the Compose port mapping to `127.0.0.1:8099:8099` for this setup. The
supplied `8099:8099` mapping binds all host interfaces. If your proxy runs
in Docker, use a private shared network and upstream `push:8099`.

Allow WebSocket upgrades on `/api/me/sync` and an idle timeout above 70 seconds.
Configure `TRUSTED_PROXIES` using the proxy addresses seen by the application;
the proxy must overwrite or append `X-Forwarded-For`.

## Accounts and devices

- Register the first administrator with `BOOTSTRAP_OTP`. It is not a TOTP code
  and cannot create another account after the first registration.
- Create invitations in Administration. Invitations expire after 24 hours.
- Register push separately on each device. On iOS/iPadOS, use the installed
  Home Screen app.
- Subscribe to topics to receive their messages. Default topics are `system`,
  `backup`, and `automation`.
- Use Account to manage passkeys, personal API keys, language, and theme.
- Use Administration to manage users, topics, integration keys, and storage.

Keep the public hostname stable: passkeys are tied to it.

## API

### Authentication

| Credential | Access |
| --- | --- |
| Personal API key | Send to its owner's account/devices; query that account's personal-key sends. Create under Account → API keys. |
| `SEND_TOKEN` | Server-wide sending and delivery status. |
| `ADMIN_TOKEN` | Token-based user, invitation, and topic administration. Does not grant browser admin access. |
| Integration key | Watchtower webhook only. Create under Administration → Integration keys. |
| Passkey session | Browser account features; administrators also get the web administration tools. |

API tokens use `Authorization: Bearer TOKEN`. Browser writes require a valid
session and an `Origin` matching `ORIGIN`.

### Send

```sh
BASE=https://push.example.com
curl --fail-with-body "$BASE/api/send" \
  -H "Authorization: Bearer $SEND_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: backup-job-0001' \
  -d '{"topic":"backup","title":"Backup","body":"Complete","longText":"All files verified."}'
```

Choose one target:

| JSON field | Target |
| --- | --- |
| `"topic":"backup"` | Topic subscribers |
| `"userId":"USER_ID"` | All devices of one user |
| `"deviceId":"DEVICE_ID"` | One device |
| `"userId":"USER_ID","deviceIds":["DEVICE_ID"]` | Selected devices of one user |
| `"broadcast":true` | All users |

Personal keys default to their owner's account when no target is supplied.
They cannot send to topics, other accounts, or broadcasts.

`body` is required, up to 2,000 bytes. Optional fields: `title` (160 bytes),
`tag` (100 bytes), and `longText` (256 KiB UTF-8). Long text is stored for the
web inbox; push notifications carry the short body.

A successful response includes `messageId`, `statusUrl`, `queued`, and
`inboxUsers`. HTTP 200 means stored and queued, not received or read.
Messages appear in the inbox even when the recipient has no push device.

`Idempotency-Key` is optional: 12–128 characters from `A–Z`, `a–z`, `0–9`,
`._:-`. Reuse it with identical content when retrying. Within seven days,
the server returns the same message instead of queuing another; different
content with the same key returns 409.

### Delivery status

```sh
curl --fail-with-body "$BASE/api/send/status/MESSAGE_ID" \
  -H "Authorization: Bearer $SEND_TOKEN"
```

Per-device results include status, attempts, next attempt time, and last error.
`accepted` means the push provider accepted the message; `confirmed` means a
browser receipt arrived. Neither means the user read it.

Transient failures retry up to eight attempts within 24 hours. Invalid
subscriptions are removed after provider responses 404/410.

### Administration API

Use `ADMIN_TOKEN` for these routes:

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/api/admin/users` | List user IDs and accounts |
| `POST` | `/api/admin/users/{id}/promote` | Promote an existing user |
| `POST` | `/api/admin/invites` | Create an invitation |
| `POST` | `/api/admin/topics` | Create a topic |

Database maintenance and central message-log routes require an administrator's
browser session, not an API token.

## CLI

```sh
sh scripts/build-cli.sh
export PUSH_BASE_URL=https://push.example.com
export PUSH_API_TOKEN='YOUR_PERSONAL_API_KEY'

./bin/pushiectl -title 'Backup' -body 'Complete'
./bin/pushiectl -device 'DEVICE_ID' -body 'Device test'
./bin/pushiectl -body 'Complete' -longtext-file ./backup.log
./bin/pushiectl -status 'MESSAGE_ID'
printf 'Disk usage is high' | ./bin/pushiectl -stdin
```

With `SEND_TOKEN` as `PUSH_API_TOKEN`, specify `-topic`, `-user`, or `-device`:

```sh
./bin/pushiectl -topic backup -body 'Complete' -key 'backup-job-0001'
```

`-key` is an idempotency key. Prefer `PUSH_API_TOKEN` over `-token` to keep
credentials out of process arguments. The CLI requires HTTPS.

Cross-compile for another server or desktop:

```sh
GOOS=linux GOARCH=arm64 sh scripts/build-cli.sh
GOOS=windows GOARCH=amd64 sh scripts/build-cli.sh
```

## Updates

Back up data and `.env`, then rebuild and recreate the service:

```sh
docker compose build
docker compose up -d --no-build push
```

Each scripted server build gets a new UTC version. The UI checks for updates
and reloads when the server version changes. Check `/api/version` or
`./bin/pushie --version`. Restarting an existing image keeps its version.

## Storage and backups

Run one server per local SQLite database. Compose stores data in `shared-data/`.
Keep this directory and `.env` when updating or moving the installation.

For a backup without SQLite tooling, stop the service and copy the entire
`shared-data/` directory, including WAL/SHM files, to a protected backup
location. Back up `.env` separately, then restart the service.

With `sqlite3` installed, a live backup can use:

```sh
sqlite3 shared-data/webpush.sqlite '.backup /backup/webpush.sqlite'
sqlite3 /backup/webpush.sqlite 'PRAGMA integrity_check;'
```

Create `/backup` first and restrict access. Do not copy only the active
SQLite file while the server is running.

For legacy JSON installations, back up `/data/webpush.json` first. If no
initialized SQLite database exists, startup imports that file from the same
data directory. The original JSON is retained; it is not kept in sync.

## Retention and maintenance

- Inboxes retain the latest 200 messages per user, including long text.
- Administration provides database statistics, cleanup previews, and compaction.
  Unread messages and active deliveries are retained by default.
- The central message log stores full content, recipients, and delivery history.
  Administrators can filter it, disable new logging, or delete older entries.
- Message-log cleanup is separate from inbox and delivery cleanup. Disabling
  logging does not delete existing entries.
- Destructive cleanup requires a preview and confirmation. The cleanup
  confirmation value is `LÖSCHEN`, including when the UI is in English.

Limits: 20 devices per user, 1,000 active deliveries per user, 10,000 globally,
and a 128 MiB serialized-state growth guard. This is a single-server tool;
large deployments need a different storage architecture.
