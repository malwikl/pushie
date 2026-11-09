# Security and deployment limits

## Deployment

- Serve through HTTPS. The supplied Compose mapping `8099:8099` exposes HTTP
  on all host interfaces. Bind to `127.0.0.1:8099:8099` for a host proxy, or use
  a private Docker network for a container proxy.
- Set `TRUSTED_PROXIES` only to your proxy CIDRs. Forwarded headers from other
  addresses are ignored.
- Keep `.env` owner-readable (`chmod 600 .env`). Protect database backups and
  setup logs, which can contain credentials.
- Keep secrets and data out of Git. Ignore rules do not remove previously
  committed credentials.
- Keep the VAPID pair and public hostname stable. Changing them can require
  device or passkey registration again.

## Access and data

- Browser access uses passkeys and secure session cookies. Writes check Origin.
- Personal API keys are account-scoped. `SEND_TOKEN` has server-wide sending
  access; `ADMIN_TOKEN` grants token-based administration.
- The administrator's central message log contains message content and
  recipients. Configure retention separately from inbox cleanup.
- Inbox content, passkeys, subscriptions, and delivery records are stored in
  SQLite. Stored message content is not encrypted at rest.
- Technical logs retain client IPs, user/device IDs, and browser identifiers.
  See [logging](LOGGING.md).

## Operating limits

Run one server per local SQLite database. A file lock rejects another server
using the same database. Shared network storage and multiple replicas are
unsupported.

Writes use a full in-memory state cache. Device, queue, and state-growth limits
bound resource use but do not make this suitable for large deployments.
Back up before updates or destructive maintenance. Dependency and container
vulnerability scans should be part of deployment checks.
