# iOS notification navigation

## Flow

1. The server sends the message ID both as legacy push data and in the
   declarative notification, with an absolute `/?message=<id>` navigation URL.
2. Browsers supporting declarative navigation can launch that URL directly.
   The app must handle it without receiving a service-worker message.
3. The legacy service-worker click handler stores the clicked ID in Cache
   Storage before navigating/focusing an existing window or opening one.
   If navigation fails, it posts the ID and focuses the existing app. The
   stored ID remains a fallback when a launch loses its URL or postMessage.
4. Startup restores the pending target before loading the inbox. On return
   to the app, visibility, focus and pageshow perform a coalesced check.
   If there is no explicit target, opening the app opens its newest unread
   message. Ordinary 30-second polling does not navigate.
5. Inbox requests run sequentially. Explicit targets win over the unread
   fallback. A newer tap supersedes navigation from an older read request.
   Duplicate signals for a just-opened message are ignored briefly.
6. The messages view becomes active, menus/dialogs close, long text expands,
   and the message scrolls into view. The selected message is marked read;
   other unread messages stay unread. The handled URL is then consumed.

## Automated verification

From the repository root:

```sh
node --test src/tests/*.cjs
(cd src && go test ./...)
```

The message-links and notification-click suites cover URL-only launches,
encoded IDs, durable recovery, expired targets, duplicate lifecycle checks,
competing taps, account changes, failed navigation, unavailable storage and
clients that disappear. These are simulated browser tests, not iPhone tests.

## Device acceptance check (requires the deployed build)

Rebuild and restart the server: the static assets are embedded in the Go
binary. Reopen the installed Home Screen app once so it can update its
service worker. Use HTTPS and an account with notifications enabled.
Record the iOS version and test on each supported major version.

Use two messages with different titles; make the tapped message older than
another unread message, and include long text in the tapped message.

| App state | Action | Expected result |
| --- | --- | --- |
| Foreground, account/settings view | Tap the banner | Messages view opens at the tapped message; long text expands |
| Background | Tap a banner or Notification Centre entry | Same specific message opens |
| Removed from app switcher | Tap a notification | Home Screen app launches at the tapped message |
| Locked phone | Tap a notification and unlock | The tapped message opens after unlocking |
| Background with two unread messages | Open using the app icon | Newest unread message opens |
| Foreground | Let polling run | Current view stays selected |
| Signed out | Tap, then sign in | The original target survives login and opens |
| Two notifications | Tap them in quick succession | Final view shows the last tapped message |
| All messages read | Open using the app icon | No arbitrary message opens |

After each notification tap, verify that only its selected message became
read and that the app does not jump to a second message a moment later.
For a failed case, capture whether the app launched, its visible URL in Safari
Web Inspector, the console error, and the responses to `/api/me/messages`
and the selected message's `/read` request. Do not share session cookies.

## Platform references

- [WebKit: Declarative Web Push](https://webkit.org/blog/16535/meet-declarative-web-push/)
- [WebKit: Web Push for Home Screen web apps](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/)
- [WebKit issue: notification click opens the app root](https://bugs.webkit.org/show_bug.cgi?id=263687)

A passing automated run does not certify notification taps on an actual
phone. Deployment and the device acceptance checks remain required.
