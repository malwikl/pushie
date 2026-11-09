# Container logs

The server writes structured JSON logs to stdout. Existing Go logger messages
are routed through the same JSON handler. After rebuilding/recreating the
container, follow the logs with:

```sh
docker logs -f --since 10m pushie
# Or from the project directory:
docker compose logs -f --tail=100 push
```

Events:

- `server.started`, `server.stopping`: process lifecycle.
- `http.request`: API reads, including HTTP status, route template, duration,
  response bytes, client IP, user ID and authentication method.
- `action`: write requests such as send, read, delete, subscribe, settings,
  login and administrative changes. HTTP status indicates success or failure.
- `auth.session.created`, `auth.session.revoked`: session lifecycle by user ID.
- `websocket.connected`, `websocket.disconnected`: live-sync connections.
- `push.state`: delivery ID, message ID, device/user IDs, previous/new states,
  attempt count and next scheduled processing time. Only committed transitions
  are logged. New deliveries start with `from: new`.
- `push.provider_response`: push provider HTTP status and retry delay. Status
  zero means no provider HTTP response was received.

Request outcomes explicitly identify `unauthenticated` (401), `forbidden`
(403), and `rate_limited` (429). Other errors use `error`. Browser sessions,
personal keys, integration keys, shared send tokens and admin tokens are
identified by method without logging the credential. The response header
`X-Request-ID` correlates an API request with its completion log; WebSocket
lifecycle events share that ID. Connection completion is logged on disconnect.

Examples:

```sh
docker logs --since 1h pushie 2>&1 | grep '"msg":"push.state"'
docker logs --since 1h pushie 2>&1 | grep '"result":"rate_limited"'
docker logs --since 1h pushie 2>&1 | grep '"message_id":"MESSAGE_ID"'
```

Request logs use registered route templates (e.g.
`PUT /api/me/messages/{id}/read`), never raw URLs or query strings. They omit
cookies, Authorization headers, session tokens, push endpoints, receipt
secrets, message bodies and request/response contents. User IDs, device IDs,
message IDs and client IPs are retained for troubleshooting. The `user_agent`
field records up to 256 printable characters of the browser's User-Agent to
help identify callers that do not send tracing IDs; other headers remain omitted.
Successful static
asset and health-check requests are omitted to reduce noise.

Compose rotates Docker logs at 10 MB per file, retaining up to three files.
Recreate the container for this logging-driver configuration to apply. No
new environment variables or database migrations are needed.

## Device tracing

API and WebSocket logs include `client_id` (random ID stored per browser
installation), `page_id` (random ID per page load, distinguishing tabs), and
`push_device_id` (the registered push-device ID, when available). A browser
and its installed Home Screen app can have separate storage and IDs. Clearing
site data resets the client ID. If storage is unavailable, it lasts only for
the current page. Push receipts report `push_device_id` from the delivery.

These are client-reported diagnostic labels, not authorization credentials or
verified hardware identities. HTTP uses bounded, validated headers; WebSockets
use query parameters because browsers cannot set custom handshake headers.
The `app_version` field identifies the build loaded by the client. The running
server's version appears in `server.started`, `/api/version`, response header
`X-App-Version`, and `pushie --version`.
