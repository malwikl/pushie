# Live device sync

Signed-in pages connect to `/api/me/sync` using WebSocket and the existing
session cookie. The server verifies the configured Origin during the
handshake and rechecks authentication before each invalidation and heartbeat.
There are at most 16 live connections per account.

After a successful SQLite commit, changes to a user's messages, read state,
devices, delivery status or profile emit a small `{"type":"sync"}` event only
to that user's connections. Topic changes notify connected users because the
topic catalogue is shared. Events contain no message bodies or credentials.
Failed transactions and unchanged state produce no events. Bursts coalesce.

The client refreshes through existing authorized HTTP endpoints, keeping the
current view selected. It reconnects with exponential backoff up to 30 seconds
and jitter, and refreshes after reconnecting to recover any missed events.
Backgrounding suspends the socket; returning to the app opens a new one. The
existing 30-second inbox poll remains a fallback. iOS background notifications
continue to use Web Push: WebSockets synchronize active pages.

The endpoint implements a narrow server-to-client WebSocket protocol using
Go's standard library, without an additional dependency. Clients send only
masked ping, pong and close control frames; mutations still use HTTP. Server
pings run every 25 seconds, with read/write deadlines and bounded frames.

## Deployment

Rebuild and restart pushie, because JavaScript assets are embedded in the
binary. Allow WebSocket upgrades on `/api/me/sync` through the reverse proxy;
configure its idle timeout above 70 seconds. For nginx, forward
`Upgrade: $http_upgrade` and `Connection: upgrade` using HTTP/1.1 on this
endpoint. The app's CSP allows its configured `ws:`/`wss:` origin.

This is an in-process connection registry for the existing single-server
SQLite deployment. Multiple server instances would require a shared event
bus. No new environment variables or database migration are required.

## Verification

Run `node --test src/tests/*.cjs` and `(cd src && go test -race ./...)`.
Tests cover the WebSocket handshake and frames over an in-memory connection,
commit/rollback behavior, account isolation, read state, coalescing, cleanup,
reconnect and lifecycle handling.

After deployment, sign in to the same account on two devices. Send, read,
and delete messages on one and verify the other updates without reloading.
Also verify a different account receives none of these events. Background
and reopen the iOS Home Screen app and verify it catches up. Confirm the
network inspector shows HTTP 101 for `/api/me/sync` through the real proxy.
