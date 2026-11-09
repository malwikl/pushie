> Pfade im historischen Review beziehen sich auf den damaligen Projektstamm; der Quellcode liegt jetzt unter `src/`. Prüfungen mit Go werden dort ausgeführt.

# Architecture and security review

Reviewed: 2026-11-08. Source/configuration review with isolated reproduction tests. This section records the original review. Remediation status is recorded below.

## Assessment

The design is suitable for a small, trusted, single-process deployment after addressing the findings below. The strongest existing controls are required WebAuthn user verification, server-side admin/ownership checks, strict Origin checks for cookie-authenticated writes, secure HttpOnly SameSite cookies, random bearer tokens, parameterized SQL, and text-only rendering of user content behind a restrictive CSP.

The main architectural weakness is that SQLite is wrapped by a complete in-memory database copy. Reads and writes serialize the entire data set, and the delivery worker processes network calls sequentially. Device growth and broadcasts amplify both costs. SQLite indexes exist but are bypassed by the main request and worker paths.

## Findings, ordered by priority

### 1. High — deployment secrets are world-readable

Evidence: the reviewed deployment's `.env` had permissions `0644`, and its parent directories allowed traversal by other host accounts. The file contains administrative and sending tokens, the VAPID private key, and setup configuration. Host-specific paths and credential contents are omitted.

Any ordinary host account able to access this filesystem can read the secrets and use the administrator token to create invitations/promote users. The VAPID key is also sensitive. This is a host-local exposure, not a demonstrated remote file-read vulnerability.

Remediation: make the file owner-readable only, restrict backup copies similarly, and rotate tokens if untrusted local accounts have had access. Keep the VAPID key stable unless there is evidence it was compromised; changing it normally requires re-registering devices.

### 2. High if reachable outside the trusted proxy network — raw HTTP published on every interface

Evidence: `compose.yaml:10` publishes `8099:8099`; `main.go:165` uses HTTP `ListenAndServe`. Docker publishes on all host interfaces when no binding address is specified. Firewall and live network exposure could not be verified.

Direct access bypasses TLS termination and any proxy-level authentication, throttling, or request limits. A token client using this HTTP address transmits credentials without transport encryption. Secure browser cookies and passkey origin checks reduce browser risk but do not protect bearer-token clients using HTTP.

Remediation: if the proxy runs on the host, publish `127.0.0.1:8099:8099`. If it runs in Docker, use a private shared network and avoid public publication. Verify access from outside the host and enforce HTTPS on the public entrypoint.

Reference: https://docs.docker.com/engine/network/port-publishing/

### 3. Medium — authenticated users can grow storage and work without bounds

Evidence: `api.go:151` registers devices without rate limiting or a per-user quota. `store_sqlite.go:293` copies the whole database under an exclusive lock on each mutation; `store.go:140` also copies it on snapshots. `send.go:182` creates one job per device without a queue limit. The inbox count limit does not bound device/job growth.

Reproduced: one ordinary user registered 100 distinct subscriptions in rapid succession; every request returned 201. An invited user can repeat this to consume memory/disk and make subsequent broadcasts and normal requests increasingly expensive. Load impact was inferred from the code, not benchmarked against production.

Remediation: enforce per-user device quotas, rate-limit account mutations, cap global/per-user pending jobs, and reject excess work before accepting a send. Replace full-store copies/scans with scoped SQL operations and due-job queries. Retention should have a predictable bound independent of future sends.

### 4. Medium — reverse proxy clients share one rate-limit bucket

Evidence: `auth.go:132` keys the 90-request/minute limiter on `RemoteAddr`; it has no trusted-proxy configuration. Authentication, send, and push receipt requests use the same limiter.

With a conventional reverse proxy, callers share the proxy address. Unauthenticated login traffic can therefore block legitimate logins, sends, and acknowledgments. Larger broadcasts can also consume the budget through acknowledgments alone.

Reproduced: 90 unauthenticated login-begin requests exhausted the same-address budget and the next request returned 429. The live proxy configuration was not available for verification.

Remediation: define trusted proxy addresses, accept forwarded client IPs only from those proxies, and separate authentication, sending, and receipt budgets. Include appropriate account/token limits and a global ceiling. Do not trust arbitrary forwarded headers.

### 5. Medium — stale push responses delete refreshed subscriptions

Evidence: `api.go:181` allows subscription replacement while keeping the device ID; `delivery.go:315` unconditionally deletes that ID after a 404/410. Despite the comment, no comparison against the subscription used by the outbound request is made.

Reproduced: the mock provider updated a device's subscription during an in-flight send, then returned 410. The refreshed registration was deleted.

Remediation: give subscriptions a generation/version. Capture it at send time, and remove or mutate a device only if the generation still matches. Apply the same rule when resolving stale responses. Add a regression test for re-registration during an in-flight request.

### 6. Medium — a second process can silently overwrite committed changes

Evidence: `store_sqlite.go:293` starts mutations from a process-local cache, rather than reading current rows in the transaction. The SQLite lock serializes SQL, but does not make cached state current. No exclusive application-instance lock is enforced.

Reproduced: process/store B saved a user's dark-theme preference. Store A then changed that user's language from its stale cache; reopening the database showed that the theme had been lost. This can affect credentials, subscriptions, and other shared payload fields too. Multiple workers can also duplicate sends.

The README already specifies single-process deployment. This is an unenforced architectural constraint rather than an exploit within the supported topology.

Remediation: immediately enforce one active instance per database with an appropriate process lock and clear startup failure. For multi-instance support, move reads/mutations and job claiming into SQL transactions with concurrency/version checks; share or externalize sessions.

### 7. Medium — sequential delivery permits head-of-line blocking

Evidence: `delivery.go:213` processes every due job serially; each outbound request has a ten-second timeout at `delivery.go:269`. Job order follows map iteration rather than queue age.

If 100 endpoints consume their timeout, one pass can take roughly 17 minutes before storage overhead. There is no fixed upper queue size, no fair ordering, and no backlog/oldest-job readiness signal. Invalid or slow subscriptions can delay unrelated recipients.

Remediation: use a bounded worker pool, ordered due-job queries and atomic job claims, per-provider concurrency limits, and metrics for oldest pending job, queue depth, and failure rates. Make shutdown cancel/drain work with a bounded deadline.

### 8. Low — logout hides sensitive UI state without clearing it

Evidence: `static/app.js:60` hides the account and returns for logged-out users. It does not clear the inbox, cached target lists, send results, cleanup preview, or invitation secret inserted at `static/app.js:190`.

On a shared browser session, previously loaded content and an unused invitation code remain in the DOM/memory after logout. This does not bypass server authorization, but leaves avoidable local data behind.

Remediation: clear sensitive DOM content, drafts, invitations, and cached account/admin state when logging out or changing identity. Guard against outstanding responses repopulating data after logout.

## Architecture improvements after the immediate fixes

- Keep the Go monolith, SQLite, and embedded assets for the current scale; a framework rewrite is unnecessary.
- Move storage behind narrow operations for account settings, devices, inbox pages, and delivery claims. Avoid exporting a complete mutable database to every handler.
- Keep the receipt-based distinction between provider acceptance and browser confirmation. Parse bounded, sanitized provider error reasons for diagnosis; the current code discards response bodies.
- Split frontend rendering, tab navigation, account state, localization, and sending into small modules. Replace runtime slicing/moving of DOM children with explicit panel structure and IDs.
- Add regression coverage for broadcasts and selected-device targeting, including ownership, duplicate IDs, empty device sets, conflicting targets, and users without devices. Current new language/theme tests do not exercise those earlier additions.
- Add account/session and credential revocation controls. Current logout revokes one session; administrators cannot conveniently disable a compromised account through the existing API/UI.
- Make container builds reproducible: copy `go.mod` and `go.sum` before dependency download, build with readonly module resolution instead of running `go mod tidy`, and pin/review base-image updates.

## Validation and limits

- Four isolated reproduction tests confirmed findings 3–6. They were run in a temporary audit workspace outside the repository and assert the problematic behavior, not a fix.
- `go vet ./...` passed.
- `go test -race ./...` passed. These tests do not invalidate the separately reproduced behavioral findings.
- No live delivery probes, destructive requests, secret rotation, or production database changes were performed.
- Docker logs, firewall rules, and live reverse-proxy configuration were inaccessible in this session.
- `govulncheck` is not installed. Dependency and container CVE scanning remain outstanding; no claim of being vulnerability-free is made. Use the official Go vulnerability database and a container scanner in CI: https://go.dev/doc/security/vuln/

## Remediation update (2026-11-08)

Historical status only: the current checkout has `.env` permissions `0644` and
Compose publishes `8099:8099` on all host interfaces. The corresponding claims
in items 1 and 2 below no longer describe this checkout. Recheck the deployed
file permissions and proxy/firewall configuration before relying on them.

Findings 1–8 have been addressed or bounded in source/configuration:

1. `.env` is now `0600`; secrets were not rotated.
2. Compose publication is restricted to host loopback. Deployment/proxy compatibility must be checked when rebuilding.
3. Device registration and account mutations are throttled; new registrations have a 20-device/user quota. Queue admission is capped at 1,000 active jobs/user and 10,000 globally. Cached state growth is capped at 128 MiB. User reads and receipt prechecks no longer copy the entire store. Full-cache writes remain an architectural limitation.
4. Configurable trusted-proxy CIDRs support right-to-left X-Forwarded-For validation; authentication, read, write, and receipt budgets are separate. Operators must configure their actual proxy addresses.
5. Subscription fingerprints prevent stale provider results from deleting refreshed registrations; stale results schedule a retry.
6. The production entrypoint holds a kernel file lock for its lifetime and rejects another server on the same database. Multi-instance storage remains unsupported.
7. Indexed, ordered queue reads feed batches of 100 jobs through four workers. Shutdown cancels sends and drains HTTP requests. Full metrics remain follow-up work.
8. Logout and identity changes clear sensitive UI/cache state, and pending responses from an old identity are rejected.

Additional changes: readonly module resolution during container builds, copying the dependency checksum file before download, sanitized bounded provider error reasons, and regression tests covering device selection/broadcast ownership, limits, proxy handling, subscription races, locks, worker concurrency, and logout races.

The architectural recommendations above for account/session revocation controls and a complete SQL-first store are not implemented in this hardening pass. No vulnerability-free claim is made. Production deployment, firewall/proxy verification, and live push tests are still required.

Post-remediation verification: `go test -race ./...`, `go vet ./...`, JavaScript syntax checks, browser-state regression checks via `node --test tests/ui-security.cjs`, and a readonly-module Go build passed. A live startup/HTTP/shutdown smoke test could not run because the execution sandbox denies socket creation. The existing running deployment was not changed.
