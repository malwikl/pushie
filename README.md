# pushie

Self-hosted Web Push mit Passkeys, Topics, persönlichem Posteingang und Remote-CLI.

## Einrichtung mit Docker

```bash
cp .env.example .env
```

Die eigene öffentliche HTTPS-Adresse (`ORIGIN`) in `.env` eintragen. Bei fehlenden oder ungültigen Schlüsseln/Tokens gibt der Container neue `NAME=value`-Zeilen aus und beendet sich. Diese in `.env` kopieren und mit `docker compose up -d --force-recreate` neu starten; siehe [Einrichtung und Betrieb](docs/README.md).

```bash
mkdir -p shared-data
sudo chown 100:101 shared-data
docker compose build
docker compose up -d
```

## Lokal bauen

Go 1.27.1 verwenden. Alle folgenden Befehle im Projekt-Hauptverzeichnis ausführen:

```bash
mkdir -p bin
sh scripts/build.sh
sh scripts/build-cli.sh
(cd src && go run . genkeys)
```

Nach Einrichtung der `.env` lokal starten:

```bash
set -a
. ./.env
set +a
./bin/pushie
```

Der Start vom Hauptverzeichnis erhält den relativen Datenbankpfad `shared-data/webpush.sqlite`.

Das Build-Skript deaktiviert VCS-Stempel, damit auch Quellarchive ohne vollständiges `.git`-Verzeichnis gebaut werden können.

## App updates

Docker Compose builds and `sh scripts/build.sh` assign a new, increasing UTC
build number (including microseconds). Restarting the same binary keeps its
version. Compose disables build caching so even unchanged source gets a new
version on each build. For direct `docker build`, use `--no-cache` as well.
Plain `go build` without version flags is an unversioned development build (`0`).

The footer, `/api/version` and the `X-App-Version` response header expose the
build number. Scripts and styles use versioned URLs. Visible pages check on
startup, return to the foreground and every minute; a newer server version
causes navigation to a fresh URL, preserving message links. A per-tab guard
prevents repeated reloads to the same version if an intermediary serves stale
HTML. Service-worker activation also upgrades older app windows when the
browser discovers the worker update. Existing legacy pages need to discover
that worker update or reload once to acquire the version checker.

An expired session (`401`) stops sync, polling and version checks, clears
in-memory account data and browser CacheStorage, and shows the login screen.
The server expires the session cookie and requests HTTP cache clearing through
`Clear-Site-Data: "cache"` in browsers supporting it. Versioned assets also
bypass stale HTTP cache entries. Signing in resumes background updates.
Ordinary permission errors (`403`) and temporary failures do not log users out.

## Prüfen

```bash
(cd src && go test ./...)
(cd src && go test -race ./...)
(cd src && go vet ./...)
node --test src/tests/*.cjs
```

## Central message log

Administrators can inspect the central message log in Administration. Filter by
full text (including long text), message ID, sender, recipient/topic, delivery
status, or UTC date range. Open an entry for full content, original targets,
recipient names and IDs, and per-device delivery attempts, timestamps and errors.
The list shows 50 entries per page; refresh reapplies the current filters.

New accepted sends are recorded atomically with the inbox and delivery queue,
including sends with no subscribers or push devices. Records remain after inbox,
account, or completed-delivery cleanup. The log uses the existing SQLite storage
capacity limit; storage cleanup currently does not delete central log records.
On upgrade, retained inbox messages and delivery jobs are recovered where
possible. Deleted historical text and unknown original sender/target information
cannot be reconstructed. The log is available only to signed-in administrators.

## Watchtower

Der Webhook `POST /api/integrations/watchtower` nimmt Watchtower-Meldungen über Shoutrrr an. [Konfiguration und Beispiele](docs/WATCHTOWER.md).

## Struktur und Dokumentation

- `src/`: Go-Modul, Server, CLI, Web-Assets und Tests.
- `docs/`: [Ausführliche Anleitung](docs/README.md) und [Sicherheitsreview](docs/SECURITY_REVIEW.md).
- `shared-data/`: lokale Datenbank; als `/data` in Docker eingebunden und von Git sowie Docker-Builds ausgeschlossen.
- `bin/`: lokale Build-Ausgaben, von Git ausgeschlossen.
- `local-artifacts/`: bisherige Programme und Archiv, von Git ausgeschlossen.

Lokale `.env`, Zugangsdaten und Datenbanken gehören nicht ins Repository. `.env.example` enthält leere Schlüssel-/Tokenvariablen mit Einrichtungskommentaren.

## Build-Skripte

Vom beliebigen Arbeitsverzeichnis aus aufrufbar (Pfade unten gelten ab Projektwurzel):

```bash
sh scripts/build.sh         # Server -> bin/pushie
sh scripts/build-cli.sh     # Remote-CLI -> bin/pushiectl
sh scripts/build-docker.sh  # Docker-Image bauen; laufender Dienst bleibt bestehen
sh scripts/build-all.sh     # Alle drei Builds

# CLI für andere Systeme bauen:
GOOS=linux GOARCH=arm64 sh scripts/build-cli.sh
GOOS=windows GOARCH=amd64 sh scripts/build-cli.sh  # bin/pushiectl.exe
```

Docker-Builds benötigen Docker Compose und die lokale `.env`. Zum Ausrollen eines
gebauten Images: `docker compose up -d --no-build push`. Das lokale Verzeichnis
`shared-data/` bleibt dabei erhalten. Build-Ausgaben in `bin/` werden nicht eingecheckt.
