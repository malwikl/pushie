# Home Assistant notification integration plan

Status: implemented locally on 2026-11-15. The custom component is maintained in the separate `/srv/pushie-home-assistant` Git repository. Server topic/verification endpoints are implemented here. Deployment and end-to-end validation on the actual Home Assistant host remain pending; no live notifications have been sent.

## Intended result

Build a Pushie custom integration that uses the HTTPS API directly and can be configured entirely through Home Assistant's UI. The user enters a URL and API token, verifies access, selects topics fetched from Pushie, and optionally sends a test notification. Each selected topic becomes a notification entity available in the visual automation editor.

Discover and target topics only. Do not fetch users, devices, or subscriptions, or offer user/device/broadcast destinations. Pushie determines the recipients from its topic subscriptions.

A custom integration is now the recommended implementation because topic discovery and guided configuration/testing are requirements. The built-in REST notifier remains a basic alternative. The CLI calls the same API and adds executable installation and maintenance.

## Existing installation and credentials

- Source and Compose configuration: `/srv/pushie`.
- Public origin: `https://pushie.new-server.kloeffer.de`. Check reachability from the actual HA host.
- Existing sending: `POST /api/send`, JSON, with `Authorization: Bearer TOKEN`.
- Topic sends accept `SEND_TOKEN` or integration keys. Personal API keys cannot send to topics; all existing integration keys also work with Home Assistant. The HA integration does not need `ADMIN_TOKEN`.
- `SEND_TOKEN` has broader server-side privileges than this topic-only integration exposes. Topic-scoped credentials can be a separate enhancement; a topic-only UI does not restrict the token itself.
- Existing browser-session topic routes are unsuitable for this API client. Do not use `/api/admin/targets`, which also returns users and devices.
- Pushie already implements durable delivery, inbox storage, provider retries, delivery status, and idempotency.
- No HA container was found on this server. Establish its location, version, and installation method before choosing the minimum supported HA version.

These observations come from the local configuration and source. End-to-end connectivity and delivery have not been tested.

## Proposed API additions

Both endpoints below require HTTPS and Bearer authentication with `SEND_TOKEN`, matching topic-send access. Apply rate limiting and Origin checks consistently with sending. Return JSON with stable machine-readable error codes and no user/device metadata or secrets. Reject account-scoped personal keys with a clear topic-permission error.

| Endpoint | Purpose | Result |
| --- | --- | --- |
| `GET /api/topics` | Fetch configured topics | Sorted names and descriptions, including topics without subscribers |
| `POST /api/verify` | Check credentials, API compatibility, and optionally a topic | Verification only, without creating or sending a notification |
| Existing `POST /api/send` | Send automation and explicit test notifications | Normal durable storage and delivery queue |

### Topic discovery

Proposed response from `GET /api/topics`:

```json
{
  "topics": [
    {"name": "automation", "description": "Automationen"},
    {"name": "backup", "description": "Backup-Ergebnisse"}
  ]
}
```

Use existing topic names as routing identifiers and descriptions as display metadata. Return an empty list when there are no topics. Do not create topics automatically or expose subscribers, users, or devices.

### Verification

`POST /api/verify` accepts `{}` to verify credentials and capabilities, or `{"topic":"automation"}` to additionally check that the topic exists and is permitted.

```json
{
  "ok": true,
  "apiVersion": 1,
  "capabilities": ["topics:list", "topics:send", "verify"],
  "topic": "automation"
}
```

Omit `topic` when none was requested. Share authentication and topic-validation rules with actual sending. Verification creates no notifications, inbox entries, delivery jobs, or idempotency records and does not contact push providers.

Define errors for malformed input or invalid topic syntax (400), invalid credentials (401), insufficient permissions (403), missing topic (404), rate limits (429), and server failures (5xx). Reject unsupported fields such as `userId`, `deviceId`, `deviceIds`, and `broadcast`.

Verification checks API access and topic availability. It does not establish that the topic has subscribers or that a device can receive a push. Public `/healthz` and `/api/version` cannot verify a token.

### Real test notifications

Use the existing `POST /api/send` with a topic, title, and body. Real tests use the same inbox and delivery path as automation notifications; no separate test-delivery endpoint is needed. Tests can notify every subscriber of the selected topic, so send only after explicit user submission.

## Home Assistant configuration and testing

1. **Add Pushie:** after installing the custom component, open Settings → Devices & services → Add integration → Pushie. Enter URL and token, with a masked token field and TLS verification enabled. Use one config entry per installation.
2. **Verify access:** call `/api/verify` before creating the entry. Show clear errors for unreachable servers, invalid tokens, missing topic permissions, and unsupported API versions. Explain when an older Pushie installation needs updating.
3. **Choose topics:** fetch `/api/topics` into a multi-select showing topic names and descriptions. Require at least one selected topic. For an empty catalog, explain how to create topics in Pushie and offer Retry/Cancel.
4. **Optional real test:** offer Send test notification or Skip. Show a single-topic dropdown limited to selected topics, plus editable title/notification defaults. Explain that this sends to that topic's subscribers. Send only on explicit submission and preserve form input on failure.
5. **Finish:** create a notify entity per selected topic, such as `notify.pushie_automation`. Show test feedback using aggregate `queued` and `inboxUsers` from the send response. Do not expose recipient identities or claim confirmed receipt.
6. **Configure later:** provide Choose topics, Refresh topics, Verify connection, and Send test notification in the integration's Configure flow. Reuse the dropdown and notification form for subsequent tests. Support URL reconfiguration and token reauthentication without removing the integration.

Verify connection never sends. Setup, restart, topic refresh, and reauthentication never send tests automatically. A test targets exactly one selected topic per submission.

## Notification entities

Implement an async `NotifyEntity` with title support for each selected topic, using HA's shared HTTP session. Map `message` to Pushie's `body`; use `Home Assistant` as the default title when omitted. Expose notifications through the standard `notify.send_message` action so users can choose a topic entity, title, and notification in the visual automation editor or HA's Actions tool.

```yaml
action: notify.send_message
target:
  entity_id: notify.pushie_automation
data:
  title: "Washing machine"
  message: "The washing machine has finished."
```

Entity IDs above are examples. Base unique IDs on a persisted installation identity and topic name, independent of credentials and URL spelling. Preserve identity through reloads, restart, token rotation, and description changes. A renamed topic is an old topic disappearing and a new topic appearing.

Refresh the catalog at startup, every five minutes, and on explicit Refresh topics. Preserve selections; new topics appear as choices without automatically enabling them. Removed selected topics become unavailable and remain visible in configuration so users can deselect or restore them. Transient refresh failures must not erase the catalog or entity registrations. A topic-not-found send response triggers refresh; a reappearing selected topic recovers its existing entity.

Validate nonblank notifications and UTF-8 byte limits: body 256 KiB, title 160 bytes. Surface the server's remaining payload-size errors, including 413, without silently truncating notifications. Additional fields such as `tag` can follow through an explicit custom action. User/device/broadcast targeting is outside this integration's scope.

## Delivery feedback and failure handling

- HTTP 200 means stored and queued. A test result can say “Stored in 2 inboxes; queued for 3 devices,” while explaining that receipt is unconfirmed. These aggregate values are feedback, not device discovery.
- Distinguish no subscribers (`inboxUsers: 0`) from inbox-only delivery (`inboxUsers > 0`, `queued: 0`). Suggest checking subscriptions or enabled push devices without listing recipients.
- Generate one idempotency key per logical send and reuse it with identical content for bounded retries after timeouts or transient server failures. Keys accept 12–128 characters from letters, digits, `._:-`; Pushie deduplicates for seven days.
- After acceptance, let Pushie's delivery worker handle provider retries. An explicit second test submission is a new logical send. Explain uncertain outcomes rather than promising exactly-once delivery across HA restarts.
- Distinguish malformed/oversized notifications, authentication, permissions, removed topics, idempotency conflicts, rate limits, network errors, and server errors. Do not retry permanent errors; respect `Retry-After` within a bounded request budget or ask the user to retry later.
- Start HA reauthentication for invalid credentials. Temporary network failures recover without reinstalling and must not permanently block sending.
- Redact tokens and notification text from logs/diagnostics. Do not store test notification text as ordinary configuration. Diagnostics may include compatibility and selected topic names, without users/devices.

## Implementation sequence

1. Add topic discovery and verification routes to Pushie, with shared authentication/topic checks, API tests, and documentation. Preserve existing send behavior.
2. Build an async client for verify, list topics, and topic send, with typed errors, timeouts, and idempotency.
3. Implement HA config/options flows, optional and repeatable tests, reauthentication, and URL reconfiguration. Keep credentials in config-entry data and topic selections in options.
4. Implement catalog refresh and stable topic notify entities, including removed-topic and recovery behavior.
5. Add focused tests, English/German UI translations, manifest validation, and manual installation instructions. HACS packaging can follow once repository/releases are established.
6. Install on the actual HA instance; verify a real test and a visual automation with an enabled receiving device.

Suggested files: `custom_components/pushie/{manifest.json,__init__.py,api.py,config_flow.py,coordinator.py,const.py,notify.py,diagnostics.py,strings.json,translations/en.json,translations/de.json}`, plus tests and documentation.

## Acceptance checks

- Discovery returns configured topic names/descriptions only, including empty and unsubscribed topics.
- Invalid credentials, personal keys, administrator-only credentials, and Watchtower keys cannot pass topic integration verification. Unsupported destination fields are rejected.
- Verification succeeds for permitted topics, rejects unknown topics, and leaves notifications, inboxes, queues, and idempotency records unchanged.
- HA verifies before saving, offers topic selection, handles an empty catalog and older API clearly, and sends tests only after explicit submission.
- Each selected topic creates a usable notify entity; real tests and visual automations notify the intended topic subscribers with accurate queue/inbox feedback.
- Tests preserve entered values after errors. Retry attempts within the same logical request reuse their idempotency key.
- Topic additions/removal/reappearance, transient refresh failures, token rotation, and HA restart preserve selections and entity identity.
- Quotes, newlines, and multibyte notifications serialize correctly; byte limits and API failures produce clear feedback.
- Run relevant Go API tests, HA client/config-flow/entity tests, and manifest validation. Record the actual HA version and end-to-end results before release.

## References

- [Pushie API and configuration](README.md#api)
- [Home Assistant config flow](https://contributors.home-assistant.io/core/integrations/config-flow/)
- [Testing credentials during configuration](https://contributors.home-assistant.io/core/quality-scale/test-before-configure/)
- [Home Assistant notify entities](https://contributors.home-assistant.io/core/entities/notify/)
- [RESTful Notifications alternative](https://www.home-assistant.io/integrations/notify.rest/)
