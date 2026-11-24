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
- Subscribe to topics to receive their notifications. Default topics are `system`,
  `backup`, and `automation`.
- Use Account to manage passkeys, personal API keys, language, and theme.
- Use Administration to manage users, topics, integration keys, and storage.

### Inbox and device topic delivery

Received and Sent share a compact list with a topic/delivery-reason pill beside
the title, date and short actions on the right, and a responsive two-line notification
preview below. An inline **… more** link reveals the full body and long text;
**less** collapses it. Preview length follows the rendered width and font size,
not a fixed character limit. An unread received notification has only a leading dot.

Clickable filter pills show counts from the notifications actually present in each
tab, including notifications from deleted topics. Direct notifications and broadcasts
have separate filters. Counts include read and unread notifications. **Mark shown as
read** affects only the selected filter; the unread badge counts the whole inbox.

Both tabs share the same toolbar. **New notification** remains available to accounts
with sending permission. The read-all action appears only in Received when the
visible notifications include unread entries. **Refresh** and **Delete all …** are
under **More actions**; with a filter selected, deletion is labeled **Delete
shown …**. Deletion confirms the number, tab, and filter and acts on that exact
selection, leaving newer incoming notifications alone. Deleting sent history does
not remove recipient inbox copies or interrupt delivery.

Under **Account settings → Reading notifications**, enable **Mark notifications as read
manually** to keep notifications unread when expanding them or tapping a push
notification. Use **Read** or **Mark all as read** explicitly. This account
preference syncs across devices. By default, opening a received notification marks it
read automatically; browsing previews or filtering never marks notifications read.
Short notifications that already fit the preview have no **more/less** controls,
including when opened from a push notification. Clicking the logo, app name,
or an empty area of the header returns to Received with all notifications visible;
the theme and navigation controls retain their own actions. The footer shows
the current browser hostname alongside the app version.

Under **Devices → Topic delivery**, choose separately for each device:

- **All subscribed topics** (default, including existing devices).
- **Selected topics**, chosen from your account's topic subscriptions.
- **No topic pushes**, for an inbox-only device.

Account subscriptions still determine which topic notifications enter your inbox.
Device settings only filter topic push delivery; direct notifications and broadcasts
remain independent. Settings apply to newly accepted notifications. Already queued
deliveries keep their original recipients. Re-registering the same push
subscription preserves its device settings.

The authenticated browser API `PUT /api/me/devices/{id}/topics` accepts
`{"topics":null}` for all subscribed topics, `{"topics":["backup"]}` for selected
topics, or `{"topics":[]}` for none. A user can update only their own devices.
`POST /api/me/notifications/read-all` accepts `{"topic":"backup"}`, or
`{"deliveryReason":"direct"}` / `{"deliveryReason":"broadcast"}`. Omit both to
select all notifications. The legacy empty topic filter selects all non-topic notifications.

### First-login setup

Accounts without registered devices see a setup guide on their first login.
It explains installation for the detected platform, then offers registration
of the current browser/app with an editable suggested name. Where the browser
exposes a device model, that model is used; otherwise a platform/browser name is
suggested. Browsers generally do not expose the user's custom operating-system
device name. Choosing **Later** dismisses the guide and saves completion; reopen
it anytime under **Devices → Set up this device**. Configured accounts are not
interrupted by onboarding after an update.
The same guide is available under **Account settings → Device setup → Restart
setup**, including after onboarding has already been completed.

- **iPhone/iPad:** add Pushie to the Home Screen and open it from that icon before
  enabling push (iOS/iPadOS 16.4 or later). The guide includes Safari Share → Add
  to Home Screen steps and prevents registration in an ordinary browser tab.
- **Android:** installation is optional in supported browsers. The guide explains
  the browser's Install app / Add to Home screen option and offers an install
  button when the browser supplies an installation prompt.
- **Mac/Windows:** installation is optional in supported browsers. The guide
  explains optional app installation or Safari's Add to Dock where available.

Notification permission is requested only after clicking **Enable push**. If
permission is denied, the guide preserves the entered name and explains how to
allow notifications in browser/system settings. Unsupported browsers can still
use the inbox. Registration uses the existing push-subscription flow.

References: [Apple Web Push requirements](https://developer.apple.com/documentation/usernotifications/sending-web-push-notifications-in-web-apps-and-browsers),
[Chrome notifications on Android](https://support.google.com/chrome/answer/3220216?co=GENIE.Platform%3DAndroid&hl=en),
and [Chrome device model hints](https://developer.chrome.com/docs/privacy-security/user-agent-client-hints).

Keep the public hostname stable: passkeys are tied to it.

## API

### Authentication

| Credential | Access |
| --- | --- |
| Personal API key | Send to its owner's account/devices; query that account's personal-key sends. Create under Account → API keys. |
| `SEND_TOKEN` | Server-wide sending and delivery status. |
| `ADMIN_TOKEN` | Token-based user, invitation, and topic administration. Does not grant browser admin access. |
| Integration key | Works with Watchtower, all API sending destinations, and Home Assistant topic discovery/verification. Create under Administration → Integration keys. |
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
  -d '{"topic":"backup","title":"Backup","body":"Complete. All files verified."}'
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

`body` is required and contains the full UTF-8 notification, up to 256 KiB.
Optional fields are `title` (160 bytes, defaults to `Push`) and `tag` (100 bytes).
Notifications longer than 2,000 bytes get an automatic short push preview; the full
text stays in the inbox. The request contains the full text in `body`; `longText` and `id` are server-managed. Integration
keys must specify exactly one destination in JSON; no topic is assumed.

A successful response includes `notificationId`, `statusUrl`, `queued`, and
`inboxUsers`. HTTP 200 means stored and queued, not received or read.
Notifications appear in the inbox even when the recipient has no push device.

`Idempotency-Key` is optional: 12–128 characters from `A–Z`, `a–z`, `0–9`,
`._:-`. Reuse it with identical content when retrying. Within seven days,
the server returns the same notification instead of queuing another; different
content with the same key returns 409.

### Delivery status

```sh
curl --fail-with-body "$BASE/api/send/status/NOTIFICATION_ID" \
  -H "Authorization: Bearer $SEND_TOKEN"
```

Per-device results include status, attempts, next attempt time, and last error.
`accepted` means the push provider accepted the notification; `confirmed` means a
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

Database maintenance and central notification-log routes require an administrator's
browser session, not an API token.

## CLI

```sh
sh scripts/build-cli.sh
export PUSH_BASE_URL=https://push.example.com
export PUSH_API_TOKEN='YOUR_PERSONAL_API_KEY'

./bin/pushiectl -title 'Backup' -body 'Complete'
./bin/pushiectl -device 'DEVICE_ID' -body 'Device test'
./bin/pushiectl -body-file ./backup.log
./bin/pushiectl -status 'NOTIFICATION_ID'
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

- Inboxes retain the latest 200 notifications per user, including long text.
- Administration provides database statistics, cleanup previews, and compaction.
  Unread notifications and active deliveries are retained by default.
- The central notification log stores full content, recipients, and delivery history.
  Administrators can filter it, disable new logging, or delete older entries.
- Notification-log cleanup is separate from inbox and delivery cleanup. Disabling
  logging does not delete existing entries.
- Destructive cleanup requires a preview and confirmation. The cleanup
  confirmation value is `LÖSCHEN`, including when the UI is in English.

Limits: 20 devices per user, 1,000 active deliveries per user, 10,000 globally,
and a 128 MiB serialized-state growth guard. This is a single-server tool;
large deployments need a different storage architecture.

### One sending API for integrations

Home Assistant, Watchtower, and other integrations use `POST /api/send` with
an integration key and the standard JSON fields `topic`, `title`, and `body`.
For example: `{"topic":"automation","title":"Example","body":"Notification text"}`.
Watchtower uses Shoutrrr's `messagekey=body` and `$topic=automation` options
so it sends the same JSON format as Home Assistant. There is no separate
Watchtower endpoint or notification format. Full notifications are accepted in `body`;
Pushie generates previews and stores long text automatically.

### Home Assistant topic API

`GET /api/topics` returns sorted `topics` containing names and descriptions.
`POST /api/verify` accepts `{}` or `{"topic":"automation"}` and returns
`ok`, `apiVersion: 1`, and capabilities `topics:list`, `topics:send`, `verify`.
These routes require `SEND_TOKEN` or an integration key; verification never sends or writes notifications.
Unknown topics return 404 and unsupported request fields return 400. Both
routes apply rate limiting and reject a mismatched browser Origin.
The separate `/srv/pushie-home-assistant` repository supplies the custom component.

To create a dedicated Home Assistant token, open Administration → Integration keys,
enter a name and create the key. Copy
the once-shown `isk.` token into HA. Revocation takes effect on subsequent requests.
These tokens allow topics, users, devices, broadcasts, and delivery status, but
not administration or browser sessions. Existing integration keys work with both
Home Assistant and Watchtower without replacement. No permission choice is needed.
The key-creation API needs only a name. Legacy `scope: "send"` and
`scope: "watchtower"` requests are still accepted and create the same key type.

### Last device activity

Each device shows its last verified activity time, or “unknown” when no proof
has been recorded. The visible browser app reports at most once per minute,
using an HMAC proof made with its push subscription's auth secret and a
session-specific challenge. The server verifies the proof, device ownership,
and browser session before persisting its own UTC timestamp. Device IDs,
User-Agent strings, account logins, and synced passkeys alone never update it.
Hidden/background pages and push deliveries do not update activity. This
identifies the registered browser installation, not physical hardware. Existing
log entries are not backfilled. The stored timestamp survives server restarts.

### Notification API naming

Inbox routes use `/api/me/notifications`; sent history uses
`/api/me/sent-notifications`; the administration log uses
`/api/admin/notification-log`. Collection responses contain `notifications`.
Send/status responses and push receipts use `notificationId`. Maintenance
requests use `notificationDays`; statistics use `notifications` and
`unreadNotifications`. The sender endpoint remains `/api/send` with `body`,
`title`, and an explicit destination. Home Assistant's own `notify.send_message`
action and `message` argument are mapped to this format by the integration.

Database schema version 3 migrates existing inboxes, delivery references,
idempotency results, and notification-log settings transactionally. No old API
aliases or duplicate tables remain after migration. Back up the database before
updating. The browser and service worker reload to the new version together.
