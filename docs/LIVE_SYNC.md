# Live device sync

Signed-in pages use `/api/me/sync` over WebSocket to refresh messages, read
state, devices, delivery status, and settings. Changes are account-scoped;
topic changes refresh the shared topic list. Background notifications use
Web Push.

The client reconnects automatically and refreshes after reconnecting.
A 30-second inbox poll remains as a fallback. There are at most 16 live
connections per account.

## Proxy requirements

- Allow WebSocket upgrades on `/api/me/sync`.
- Set the idle timeout above 70 seconds.
- For nginx, forward `Upgrade: $http_upgrade` and `Connection: upgrade` using
  HTTP/1.1 on this endpoint.

## Verify

Sign in on two devices. Send, read, and delete a message on one; verify the
other updates without reloading. Background and reopen the app, then check
that it catches up. The proxy should return HTTP 101 for `/api/me/sync`.
