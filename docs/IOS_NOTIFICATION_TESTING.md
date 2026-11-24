# iOS notification checks

Use the deployed HTTPS site, add it to the Home Screen, and enable push in
the installed app. Reopen the app after deployment to update its service worker.

Send two notifications with different titles. Include long text in the older one.

| App state | Action | Expected result |
| --- | --- | --- |
| Foreground, another view | Tap a notification | Its notification opens with long text expanded |
| Background | Tap a notification | The selected notification opens |
| Removed from app switcher | Tap a notification | The installed app opens at the selected notification |
| Phone locked | Tap and unlock | The selected notification opens |
| Signed out | Tap, then sign in | The original notification opens after login |
| Two unread notifications | Open the app icon | The newest unread notification opens |
| Two notifications | Tap both quickly | The last selected notification stays open |
| All notifications read | Open the app icon | No arbitrary notification opens |

Only the opened notification should be marked read. Polling should not move the
current view to another notification.

For failures, record the iOS version, app state, visible URL, console error,
and relevant API status codes. Do not include session cookies or tokens.

JavaScript regression tests run with `node --test src/tests/*.cjs`; they do
not replace testing on a device.
