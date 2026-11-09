# Pushie

Self-hosted Web Push with passkeys, topics, a personal inbox, and a remote CLI.

## Features

- Push notifications for phones, tablets, and desktop browsers.
- Passkey login, invitation-based registration, and multiple devices per account.
- Send to topics, users, selected devices, or all users.
- Persistent inbox with read state, long messages, and live device sync.
- Delivery queue with retries, status tracking, and idempotent API requests.
- REST API, standalone CLI, and Watchtower webhook.
- Admin tools for users, topics, message logs, and database maintenance.
- Installable web app with English/German UI and light/dark themes.

## Quick start

Requires Docker Compose, a public HTTPS domain, and a reverse proxy.
Run commands from the project root.

```sh
cp .env.example .env
chmod 600 .env
mkdir -p shared-data
sudo chown 100:101 shared-data
docker compose build
```

Set `ORIGIN` and the contact email in `VAPID_SUBJECT` in `.env`.
Keep the credential fields empty for the first start:

```sh
docker compose up -d
docker compose logs --no-log-prefix push
```

The container prints generated `NAME=value` assignments and exits. Copy them
into `.env`, then restart:

```sh
docker compose up -d --force-recreate
```

Point your HTTPS reverse proxy at port `8099`. The supplied Compose file
publishes this port on all host interfaces. For a proxy on the same host,
change the mapping to `127.0.0.1:8099:8099`.

Open your domain, register the first administrator using `BOOTSTRAP_OTP`, and
enable push on each device. On iPhone/iPad, add the site to the Home Screen
and open the installed app first. Clear `BOOTSTRAP_OTP` after registration
and recreate the container. Invite other users from Administration.

## Send a message

Create a personal API key under **Account → API keys** to send to your account:

```sh
curl --fail-with-body https://push.example.com/api/send \
  -H "Authorization: Bearer $PUSH_API_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"title":"Backup","body":"Backup complete"}'
```

Use the server's `SEND_TOKEN` for topic or cross-account sends.
See [API and CLI usage](docs/README.md#api).

## Build and test

Requires Go 1.27.1. Node.js is used for JavaScript tests.

```sh
sh scripts/build.sh          # bin/pushie
sh scripts/build-cli.sh      # bin/pushiectl
sh scripts/build-docker.sh   # Compose image; does not restart the service
sh scripts/build-all.sh      # All builds

(cd src && go test -race ./...)
(cd src && go vet ./...)
node --test src/tests/*.cjs
```

For local startup, configure `.env`, then run from the project root:

```sh
set -a
. ./.env
set +a
./bin/pushie
```

## Documentation

- [Configuration, API, CLI, updates, and backups](docs/README.md)
- [Watchtower integration](docs/WATCHTOWER.md)
- [Logs and troubleshooting](docs/LOGGING.md)
- [Live sync and proxy requirements](docs/LIVE_SYNC.md)
- [iOS notification checks](docs/IOS_NOTIFICATION_TESTING.md)
- [Security and deployment limits](docs/SECURITY_REVIEW.md)

Source lives in `src/`. Docker stores data in `shared-data/`, mounted at `/data`.
Credentials, databases, and local build outputs are excluded from Git.
