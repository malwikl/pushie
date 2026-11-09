# iOS notification checks

Use the deployed HTTPS site, add it to the Home Screen, and enable push in
the installed app. Reopen the app after deployment to update its service worker.

Send two messages with different titles. Include long text in the older one.

| App state | Action | Expected result |
| --- | --- | --- |
| Foreground, another view | Tap a notification | Its message opens with long text expanded |
| Background | Tap a notification | The selected message opens |
| Removed from app switcher | Tap a notification | The installed app opens at the selected message |
| Phone locked | Tap and unlock | The selected message opens |
| Signed out | Tap, then sign in | The original message opens after login |
| Two unread messages | Open the app icon | The newest unread message opens |
| Two notifications | Tap both quickly | The last selected message stays open |
| All messages read | Open the app icon | No arbitrary message opens |

Only the opened message should be marked read. Polling should not move the
current view to another message.

For failures, record the iOS version, app state, visible URL, console error,
and relevant API status codes. Do not include session cookies or tokens.

JavaScript regression tests run with `node --test src/tests/*.cjs`; they do
not replace testing on a device.
