# Logs and troubleshooting

The server writes JSON logs to stdout. Compose rotates logs at 10 MB per file,
retaining three files.

```sh
docker compose logs -f --tail=100 push
docker logs --since 1h pushie 2>&1 | grep '"msg":"push.state"'
docker logs --since 1h pushie 2>&1 | grep '"result":"rate_limited"'
```

| Event | Details |
| --- | --- |
| `server.started`, `server.stopping` | Startup, shutdown, server version |
| `http.request`, `action` | Request route, status, duration, caller, outcome |
| `auth.session.created`, `auth.session.revoked` | Session lifecycle |
| `websocket.connected`, `websocket.disconnected` | Live-sync connections |
| `push.state` | Delivery status transitions and attempts |
| `push.provider_response` | Provider HTTP status and retry delay |

Use `X-Request-ID` to correlate an API response with logs. Delivery events
include message, user, and device IDs. `client_id`, `page_id`, and
`push_device_id` identify browser installations, tabs, and registered devices;
these are diagnostic labels, not verified identities.

Technical request logs omit authorization headers, cookies, raw query strings,
message bodies, and push endpoints. They retain client IPs, user/device IDs,
and a bounded User-Agent value. Initial setup output includes newly generated
credentials. The admin message log separately stores message content.

## Common checks

- **Startup exits:** copy generated credentials into `.env`, then recreate the
  container. Check `ORIGIN`, the VAPID pair, and `shared-data/` permissions.
- **401:** sign in again or check the API token. **403:** check target permissions
  and the configured Origin. **429:** respect `Retry-After`.
- **No push:** verify the browser granted permission, the device is registered,
  and the account subscribes to the target topic. Inspect delivery status.
  Provider acceptance does not guarantee device receipt.
- **Devices do not sync:** verify WebSocket upgrades on `/api/me/sync`.
- **Old UI:** confirm `/api/version` changed after rebuilding, then reopen the app.

Check `GET /healthz` for service health. Avoid sharing credentials or unredacted
logs when reporting an issue.
