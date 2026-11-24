# Watchtower integration

Send Watchtower updates to `POST /api/send` using
Shoutrrr's generic JSON template.

## Setup

1. Create a key under **Administration → Integration keys**. Copy it when shown.
2. Subscribe the receiving accounts to the `automation` topic.
3. Create `secrets/watchtower-notification-url.txt` with this single line,
   replacing the domain and key:

```text
generic://push.example.com/api/send?template=json&title=Watchtower&messagekey=body&$topic=automation&@Authorization=Bearer%20YOUR_INTEGRATION_KEY
```

Add the notification secret to your Watchtower Compose service:

```yaml
services:
  watchtower:
    image: nickfedor/watchtower:1.24.0
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      WATCHTOWER_NOTIFICATION_URL: /run/secrets/watchtower_notification_url
    secrets:
      - watchtower_notification_url

secrets:
  watchtower_notification_url:
    file: ./secrets/watchtower-notification-url.txt
```

`generic://` uses HTTPS. The secret file contains a credential and is excluded
from Git and Docker builds. URL-encode special characters in the key.

## Targets and authentication

- Use the same integration keys as Home Assistant. Existing integration keys keep working.
- The `$topic=automation` URL option adds `"topic":"automation"` to the JSON.
  Replace it with `$userId=USER_ID` or `$deviceId=DEVICE_ID` for direct delivery.
- Personal API keys remain limited to their owner's account and devices.
- `SEND_TOKEN` also works. `ADMIN_TOKEN` and browser sessions cannot send here.

The payload is `{"topic":"automation","title":"Watchtower","body":"Container updated"}`.
`messagekey=body` makes Shoutrrr use the standard field name; no separate
Watchtower format or endpoint is needed. Destination fields belong in JSON,
not forwarded URL query parameters.

Notifications can contain up to 256 KiB of UTF-8 text. Full reports are stored in
the inbox; notifications over 2,000 bytes get a shortened push preview. HTTP 200
means stored and queued. Do not send `notification` or `longText`.

## Test

```sh
curl --fail-with-body https://push.example.com/api/send \
  -H "Authorization: Bearer $INTEGRATION_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"topic":"automation","title":"Watchtower test","body":"Container updated"}'
```

Check a subscriber's inbox and push notification.
