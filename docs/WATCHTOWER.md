# Watchtower integration

Send Watchtower updates to `POST /api/integrations/watchtower` using
Shoutrrr's generic JSON template.

## Setup

1. Create a key under **Administration → Integration keys**. Copy it when shown.
2. Subscribe the receiving accounts to the `automation` topic.
3. Create `secrets/watchtower-notification-url.txt` with this single line,
   replacing the domain and key:

```text
generic://push.example.com/api/integrations/watchtower?template=json&title=Watchtower&@Authorization=Bearer%20YOUR_INTEGRATION_KEY
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

- Integration keys default to topic `automation`. Append one of `&topic=system`,
  `&userId=USER_ID`, or `&deviceId=DEVICE_ID` to change the target.
- Personal API keys default to their owner's account and cannot target topics
  or other accounts.
- `SEND_TOKEN`, `ADMIN_TOKEN`, and browser sessions are not accepted here.

The payload is `{"title":"Watchtower","message":"Container updated"}`.
Messages can contain up to 256 KiB; full reports are stored in the inbox and
long reports get a shortened push preview. HTTP 200 means stored and queued.

## Test

```sh
curl --fail-with-body https://push.example.com/api/integrations/watchtower \
  -H "Authorization: Bearer $INTEGRATION_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"title":"Watchtower test","message":"Container updated"}'
```

Check a subscriber's inbox and push notification.
