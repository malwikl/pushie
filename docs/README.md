# pushie

Eine kleine Go-Anwendung für **Push-Nachrichten auf eigene iPhones, iPads, Macs und Browser**. Mit **Passkeys**, einmaligem Administrator-Setup-Code aus `.env`, **Topic-Abonnements**, **persönlicher Nachrichten-Inbox**, persistenter **SQLite-Retry-Queue**, geschützter **Admin-Weboberfläche mit SQLite-Statistik und Bereinigung**, API für andere Server und einer **Remote-CLI**.

> **Teststatus:** Go-Tests einschließlich Race-Prüfung, `go vet` und JavaScript-Tests wurden erfolgreich ausgeführt. Reale Passkey- und Push-Abläufe sowie eine visuelle Browserprüfung sind separat zu prüfen.

Alle Befehle gelten vom Projekt-Hauptverzeichnis aus, sofern kein `cd` angegeben ist. [Kurzanleitung](../README.md).

Domains, E-Mail-Adressen, Benutzer- und Geräte-IDs in den Beispielen sind
Platzhalter. `push.example.com` und `admin@example.com` durch die eigenen
Angaben ersetzen; Zugangsschlüssel ausschließlich lokal konfigurieren.

## Watchtower

[Watchtower über den JSON-Webhook anbinden](WATCHTOWER.md).

## Funktionen

- Der **erste Administrator** registriert sich mit `BOOTSTRAP_OTP` aus der Konfiguration und erzeugt dabei einen **Passkey**. Der Einrichtungscode kann danach nie wieder zur Kontoanlage verwendet werden, auch nach Neustarts nicht.
- Weitere Benutzer benötigen eine **24 Stunden gültige Einladung**. Administratoren erstellen diese im Web-Interface oder über die Admin-Token-API.
- Login ohne Passwörter: Passkeys mit Face ID, Touch ID oder FIDO2-Sicherheitsschlüssel. Pro Konto können mehrere Passkeys hinterlegt werden.
- Jeder Nutzer kann **Topics abonnieren und abbestellen**. Alle seine registrierten Geräte erhalten anschließend Push-Mitteilungen für diese Topics.
- Administratoren können im Browser unter `/admin` oder `/` eine Nachricht an ein **Topic** oder einen **Benutzer** schicken und neue Topics/Einladungen erstellen.
- **Andere Server** können `POST /api/send` mit einem separaten Bearer-Token aufrufen. Die enthaltene `src/cmd/pushiectl`-CLI arbeitet über dasselbe API.
- **Jeder Benutzer hat eine eigene Inbox**, auch wenn er kein aktives Push-Gerät hat. Die **letzten 200 Nachrichten pro Benutzer** werden dauerhaft in SQLite gespeichert; optionale Langtexte bis 256 KiB werden in einer eigenen Textspalte abgelegt und erscheinen nur im Web-Posteingang. Markieren als gelesen, alle als gelesen markieren oder einzelne Nachrichten löschen.
- Direkter Versand an `topic`, `userId` oder `deviceId`; genau **eines** dieser Felder ist Pflicht. Beim gerätespezifischen Versand wird die Nachricht im Posteingang des Gerätebesitzers gespeichert.
- **Dauerhafte Retry-Queue** pro Gerät, automatisches Wiederaufnehmen nach Neustart, exponentieller Backoff, `Retry-After`-Beachtung und Abschluss bei Annahme durch den Push-Dienst.
- **Zustellstatus** pro Gerät in der Inbox und im Admin-Interface; externe API und CLI können den Status abfragen.
- **Idempotency-Key** für den Versand von anderen Servern verhindert doppelte Nachrichten bei wiederholten Requests.
- Automatisches Entfernen ungültiger Push-Abonnements bei Provider-Antworten 404/410.
- **Admin-Speicherverwaltung**: Datenbankstatistik, Push-Zustellstatus, Dateigrößen von SQLite/WAL/SHM, Integritätscheck, Löschvorschau, bewusst bestätigte Bereinigung und separater WAL-Checkpoint mit `VACUUM`.

## Upgrade: JSON → SQLite (Version 4)

1. **Vor dem Upgrade ein Backup des bisherigen Docker-Volumes** bzw. der Datei `/data/webpush.json` erstellen. Das alte Setup bis zum erfolgreichen Abschluss nicht entfernen.
2. Bei alten Compose-Installationen zuerst das bestehende Volume in `shared-data/` übernehmen (siehe unten). **Wichtig:** Docker Compose versieht Volumes normalerweise mit dem Projektnamen als Präfix. Beim Start aus einem anderen Entpackverzeichnis daher denselben Compose-Projektnamen wie bisher nutzen (`docker compose -p <alter-projektname> up -d --build`) oder das vorhandene Volume ausdrücklich zuordnen, sonst entsteht eine leere neue Datenbank. Compose setzt `DB_PATH=/data/webpush.sqlite` und verwendet den alten Dateipfad nicht mehr als aktive Datenbank.
3. Beim **ersten Start**, wenn `/data/webpush.sqlite` noch nicht initialisiert ist, liest der Server automatisch `/data/webpush.json` aus demselben Verzeichnis und übernimmt Benutzer inkl. Passkeys und Rollen, Topics, Geräte, Einladungen, alle gespeicherten Inbox-Nachrichten, Zustellungsjobs, Retry-Zeiten, Quittungen, Idempotency-Keys und das bereits verbrauchte Bootstrap-Kennzeichen. **Das JSON-Original wird nicht gelöscht oder verändert.**
4. Die Migration erfolgt in einer SQLite-Transaktion. Schlägt sie fehl, startet der Server nicht mit einer leeren Ersatzdatenbank; den Fehler prüfen und beheben. Die neue SQLite-Datei nicht manuell über vorhandene Daten kopieren.
5. Erfolg verifizieren: Anmeldung per bestehendem Passkey, Geräte/Topics, Inbox, Status und `GET /healthz` prüfen. Erst dann regulär weiterarbeiten.

**Achtung:** Liegt eine bereits initialisierte SQLite-Datenbank vor, wird eine alte JSON-Datei nicht erneut importiert. Bei der erstmaligen Umstellung sollten **alte und neue Server nie parallel** Nachrichten entgegennehmen, sonst könnten während der Migration noch neue Einträge in JSON entstehen, die SQLite nicht übernommen hat. Die JSON-Datei ist ein Backup, kein parallel synchronisierter Speicher.

Die bisherige Begrenzung von 200 Inbox-Nachrichten pro Benutzer bleibt vorerst bestehen. Vollständige Langtexte werden nach dem Überschreiten dieses Limits zusammen mit den ältesten Nachrichten gelöscht.

## Voraussetzungen

- Öffentliche **HTTPS-Domain** mit stabilem Hostnamen, z. B. `https://push.example.com`. Die Domain wird für Passkeys verwendet und darf nicht ohne Migrationsplanung geändert werden.
- Docker + Compose oder Go **1.27.1** für die serverseitigen Abhängigkeiten aus `go.mod`.
- HTTPS-Reverse-Proxy, etwa Caddy, Traefik oder nginx. Der Compose-Server lauscht nur auf `127.0.0.1:8099` des Hosts.
- iPhone/iPad ab iOS/iPadOS 16.4: Webseite **zum Home-Bildschirm hinzufügen** und die installierte Web-App öffnen, dort Push aktivieren. Für macOS ist Web Push in kompatiblen Browsern möglich.
- Ausgehender HTTPS-Zugriff auf die Push-Dienste der Browserhersteller (APNs, FCM, Mozilla). **Vollständig ohne deren Push-Infrastruktur funktioniert Web Push nicht.**

## 1. Konfigurieren

```bash
cp .env.example .env

# ORIGIN und die Kontaktadresse VAPID_SUBJECT in .env anpassen.
# Schlüssel und Tokens zunächst leer lassen.
docker compose up -d --build
docker compose logs --no-log-prefix push
# Generierte NAME=value-Zeilen in .env kopieren, dann:
docker compose up -d --force-recreate
```

Fehlende oder ungültige VAPID-Schlüssel und Tokens werden beim Start neu erzeugt und als kopierbare `NAME=value`-Zeilen ausgegeben. Der Server startet erst nach Übernahme der Werte in `.env` und Neuerstellung des Containers. Gültige bestehende Secrets werden nicht ausgegeben. Ein leeres `BOOTSTRAP_OTP` bleibt bei ansonsten gültigen Zugangsdaten deaktiviert.

In `.env` mindestens folgende Variablen korrekt eintragen:

```dotenv
VAPID_PUBLIC_KEY=...                 # Ausgabe von genkeys
VAPID_PRIVATE_KEY=...                # Ausgabe von genkeys
VAPID_SUBJECT=mailto:admin@example.com # Eigene Kontaktadresse einsetzen
SEND_TOKEN=...                       # >= 32 Zeichen
ADMIN_TOKEN=...                      # >= 32 Zeichen, anders als SEND_TOKEN
BOOTSTRAP_OTP=...                    # 20-256 Zeichen; nur für erste Registrierung
ORIGIN=https://push.example.com
ADDR=127.0.0.1:8099
DB_PATH=shared-data/webpush.sqlite
```

`ORIGIN` ist die exakte öffentliche HTTPS-Adresse aus dem Browser (ohne abschließenden Slash). Der Passkey-Hostname wird daraus automatisch ohne Protokoll und Port abgeleitet; eine alte `RP_ID`-Variable wird nicht mehr verwendet. Der VAPID-Kontakt verwendet standardmäßig ebenfalls `ORIGIN`; optional kann `VAPID_SUBJECT` eine eigene `mailto:`- oder HTTPS-Kontaktadresse setzen. `BOOTSTRAP_OTP` ist **kein zeitbasierter TOTP-Code**: Es ist ein zufälliger, **einmalig verwendbarer Einrichtungscode**. Nach erfolgreicher Einrichtung ist sein Verbrauch in der Datenbank gespeichert. Entferne den Wert danach aus `.env` und starte den Server neu. Registrierte Admin-Passkeys bleiben erhalten.

**Nie Tokens, Einrichtungscode oder VAPID-Private-Key in JavaScript, URLs, Logs oder öffentliches Git einchecken.**

Go lädt `.env` nicht von selbst. Für lokalen Betrieb statt Compose: `set -a; . ./.env; set +a; ./bin/pushie`.

Für einen lokalen Server-Build mit neuer Versionsnummer: `sh scripts/build.sh`; anschließend `./bin/pushie` starten. Die Remote-CLI wird als `pushiectl` gebaut (siehe unten). Compose setzt `DB_PATH=/data/webpush.sqlite` unabhängig vom lokalen Pfad in `.env`.

## 2. Starten

```bash
mkdir -p shared-data
sudo chown 100:101 shared-data
docker compose up -d --build
```

Caddy-Beispiel auf demselben Server:

```caddyfile
push.example.com {
  reverse_proxy 127.0.0.1:8099
}
```

Im Docker-Compose-Setup liegt die aktive SQLite-Datenbank im lokalen Ordner `shared-data/` neben `compose.yaml`, im Container unter `/data/webpush.sqlite`. Der Ordner ist von Git und Docker-Builds ausgeschlossen. Der Bind-Mount erfordert einen vorhandenen Ordner (UID 100, GID 101 für den Containerbenutzer); ein fehlender Ordner führt absichtlich zu einem Startfehler statt einer unbemerkten leeren Datenbank. Beim lokalen Betrieb muss der ausführende Benutzer Schreibrechte auf den Ordner haben. SQLite legt im WAL-Modus bei Bedarf außerdem `webpush.sqlite-wal` und `webpush.sqlite-shm` an. **Nur eine Go-Serverinstanz** pro Datenbank betreiben: Der Dienst hält weiterhin einen prozesslokalen Lesecache und ist nicht auf mehrere Instanzen ausgelegt.

### Bestehendes Docker-Volume nach shared-data übernehmen

Den alten Container vor dem Wechsel des Mounts stoppen. Den gesamten Inhalt einschließlich SQLite-WAL/SHM und eventuell vorhandener JSON-Datei kopieren; niemals eine vorhandene Zieldatenbank überschreiben:

```bash
test ! -e shared-data || exit 1
docker compose stop push
docker cp -a pushie:/data ./shared-data
sudo chown -R 100:101 shared-data
# Erst jetzt compose.yaml auf den Bind-Mount umstellen.
docker compose up -d --no-build --force-recreate push
```

Vor dem Neustart eine separate Kopie des gestoppten Datenverzeichnisses sichern. Das bisherige Volume als Rückfallkopie behalten und nicht mit `docker volume prune` entfernen. Nach dem Start Healthcheck, Anmeldung und vorhandene Daten prüfen. Für einen Rollback den Dienst stoppen und den alten Volume-Mount wiederherstellen; nach der Umstellung neu geschriebene Daten müssen dabei separat übernommen werden.

### SQLite-Persistenz und Backup

Die Daten werden in separaten Tabellen `users`, `topics`, `devices`, `invites`, `messages`, `deliveries`, `idempotency` und `metadata` gespeichert. Lange Inhalte stehen in `messages.long_text`, die übrigen strukturierten Felder als JSON-Payloads pro Datensatz. Transaktionen schreiben nur geänderte Datensätze. SQLite wird mit WAL, `foreign_keys=ON`, `busy_timeout=5000` und `synchronous=FULL` betrieben; die Datenbankdatei hat Modus `0600`.

**Konsistente laufende Backups** mit dem SQLite-Tool erzeugen, z. B. bei Zugriff auf den Datenbankpfad:

```bash
sqlite3 shared-data/webpush.sqlite '.backup /backup/webpush-2026-11-07.sqlite'
sqlite3 /backup/webpush-2026-11-07.sqlite 'PRAGMA integrity_check;'
```

Nicht bei laufendem Dienst einfach nur `webpush.sqlite` mit `cp` kopieren: noch nicht checkpointete Transaktionen können im `-wal` liegen. Falls kein SQLite-Tool vorhanden ist, den Server kontrolliert stoppen und das **gesamte** Datenverzeichnis inklusive eventuell vorhandener WAL-Dateien sichern. VAPID-Secrets und `.env` separat sicher sichern.

## SQLite-Statistiken und Datenbankbereinigung (Version 5)

Ein **per Passkey angemeldeter Administrator** findet im Adminbereich unter `https://push.example.com/admin` den Abschnitt **„SQLite-Datenbank: Statistiken und Wartung“**. Normale Benutzer sehen ihn nicht. Die Endpunkte sind ebenfalls nur über eine gültige Administrator-Session zugänglich (schreibende Requests mit exakt passender `Origin`). `SEND_TOKEN` und `ADMIN_TOKEN` gewähren **keinen** Zugriff auf diese Web-Session-Endpunkte.

**Statistik (`GET /api/admin/storage/stats`)**: Anzahl Benutzer, Geräte, Topics und Topic-Abos; persönliche Nachrichten und ungelesene Einträge; Zahl der Push-Aufträge mit Status `pending`, `sending`, `retry`, `accepted`, `confirmed`, `unconfirmed`, `failed`; Langtextgröße; Einladungen und Idempotenzschlüssel; SQLite-Hauptdatei, WAL und SHM sowie freie Seiten und `PRAGMA quick_check`. Die Nachrichtenzahl zählt pro Empfänger gespeicherte Inbox-Einträge – eine Topic-Nachricht kann bei mehreren Empfängern mehrfach zählen.

**Bereinigen – immer manuell:**

1. Frist für **alte Nachrichten** auswählen (Vorgabe 90 Tage) und für **abgeschlossene Zustellprotokolle** (Vorgabe 30 Tage). Zulässige Werte: 1–3650 Tage.
2. **„Löschvorschau berechnen“** zeigt genau, wie viele Zeilen nach den aktuellen Kriterien entfernt würden. Die Vorschau löscht nichts.
3. Standardmäßig werden **nur gelesene** alte Nachrichten gelöscht. Optional lässt sich die Löschung auch ungelesener Nachrichten ausdrücklich aktivieren. Keine Löschung von Nachrichten, solange aktive Push-Aufträge oder eine noch gültige siebentägige Idempotenz-Zuordnung darauf verweisen.
4. Push-Aufträge werden nur bereinigt, wenn ihr Status **`accepted`, `confirmed`, `failed` oder `unconfirmed`** ist und sie älter als die gewählte Frist sind. `pending`, `sending` und `retry` bleiben **immer** erhalten, selbst wenn sie älter sind.
5. Bei der Bereinigung werden zusätzlich **abgelaufene Einladungen** und Idempotenzschlüssel nach **mehr als sieben Tagen** entfernt. Die Frist schützt Wiederholungen der Remote-API vor versehentlicher Doppelverarbeitung.
6. Zum Ausführen `LÖSCHEN` eingeben, den Löschen-Button drücken und die zweite Nachfrage bestätigen. Alle Löschungen erfolgen **atomar in einer SQLite-Transaktion**. Nach der Bereinigung die Statistik neu laden und bei Bedarf separat **„SQLite optimieren (VACUUM)“** ausführen.

**Speicher freigeben:** Gelöschte Datensätze reduzieren nicht zwangsläufig sofort die Dateigröße. `POST /api/admin/storage/compact` führt einen WAL-Checkpoint und `VACUUM` aus (separate Bestätigung `OPTIMIEREN`). Das kann den Server kurz blockieren und benötigt vorübergehend zusätzlichen Festplattenspeicher. `VACUUM` löscht keine weiteren Nachrichten. **Vor jeder Löschung ein konsistentes Backup anlegen**, insbesondere wenn auch ungelesene Nachrichten entfernt werden sollen.

| Methode | Endpunkt | Zweck |
|---|---|---|
| `GET` | `/api/admin/storage/stats` | Statistiken abrufen |
| `POST` | `/api/admin/storage/preview` | Löschvorschau, ohne Änderungen |
| `POST` | `/api/admin/storage/cleanup` | Daten löschen (Feld `confirm: "LÖSCHEN"`) |
| `POST` | `/api/admin/storage/compact` | WAL-Checkpoint und VACUUM (Feld `confirm: "OPTIMIEREN"`) |

Vorschau und Bereinigung verwenden die JSON-Felder `messageDays`, `deliveryDays`, `deleteUnread` (boolesch). Die Bereinigung berechnet die Auswahl unter der Datenbanksperre **neu**, damit neu eingegangene Push-Aufträge geschützt bleiben. Daher kann die tatsächlich gelöschte Zahl von einer älteren Vorschau abweichen. Es werden keine Benutzer, Geräte, Topics oder Passkeys entfernt.

## 3. Ersten Benutzer mit Einrichtungscode anlegen

1. Öffne `https://push.example.com` mit dem Gerät, das den Passkey erzeugen soll.
2. Unter **Einmalige Einrichtung** den Namen und `BOOTSTRAP_OTP` aus der Serverkonfiguration eintragen.
3. **Administrator mit Passkey anlegen** drücken. Die Browser-Abfrage für Face ID/Touch ID/Sicherheitsschlüssel abschließen.
4. Anschließend als Admin angemeldet: Topics abonnieren und **Push auf diesem Gerät aktivieren**.
5. Auf einem zweiten Gerät mit dem Passkey anmelden und dessen Browser-Push-Abonnement separat registrieren.
6. Nach Möglichkeit einen zweiten Passkey als Wiederherstellungsoption hinzufügen. Den Einrichtungscode aus `.env` entfernen.

Die Kontoerstellung verbraucht den Setup-Code **erst nach erfolgreicher WebAuthn-Verifikation**, in derselben atomaren Datenbanktransaktion wie die Kontoanlage. Ist schon ein Konto vorhanden oder wurde der Code verbraucht, ist der Bootstrap-Endpunkt nicht mehr nutzbar. Bei einem abgebrochenen Passkey-Vorgang kann derselbe Code bis zum erfolgreichen Abschluss erneut verwendet werden.

## 4. Admin-Weboberfläche

`https://push.example.com/admin` (oder die Startseite) öffnen und als Administrator per Passkey anmelden. Die Oberfläche enthält:

- **Nachricht senden:** Zieltyp **Topic** oder **Benutzer**, Empfänger, Titel und Nachricht eingeben. Der Server legt die Nachricht im Posteingang und je Gerät in einer dauerhaften Queue ab. Der Hintergrund-Worker versendet und wiederholt bei Bedarf. Im Admin-Interface kann anschließend der Gerätestatus aktualisiert werden.
- **Benutzer einladen:** Namen eingeben und den einmaligen Einladungscode sicher weitergeben; der neue Benutzer registriert damit seinen Passkey.
- **Topic anlegen:** Slug (z. B. `homelab`) und optionale Beschreibung; Benutzer können dieses Topic danach selbst abonnieren.

Der Web-Admin-Versand verwendet das Passkey-Login mit einem serverseitigen Admin-Rollencheck und einen geschützten Sitzungscookie. Der `ADMIN_TOKEN` oder `SEND_TOKEN` wird **nicht** im Browser eingegeben.

### Vorhandene Installation (Migration von v2)

Bei bestehenden Konten ist der Erstregistrierungs-Code absichtlich deaktiviert. Es gibt keinen automatischen Admin, da sonst bestehende Konten ihre Berechtigungen unbemerkt ändern würden. Stattdessen einen existierenden Benutzer einmalig über das bestehende Admin-Token hochstufen:

```bash
BASE=https://push.example.com
curl -fsS "$BASE/api/admin/users" \
  -H "Authorization: Bearer $ADMIN_TOKEN"
# -> die gewünschte userId aus der Antwort kopieren

curl -fsS -X POST "$BASE/api/admin/users/<USER_ID>/promote" \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

Danach erneut im Browser per Passkey anmelden bzw. die Seite aktualisieren. Bestehende Passkeys, Topics, Geräte und Einladungen bleiben erhalten. Für einen **frischen Server** stattdessen ausschließlich `BOOTSTRAP_OTP` verwenden.

## 5. Externe Push-API (anderer Server)

Die externe API akzeptiert `SEND_TOKEN` oder einen persönlichen API-Schlüssel als Bearer-Token. Sie benötigt weder Browser-Sitzung noch Benutzer-Passkey. Sichere den Token als Secret auf dem aufrufenden Server. `SEND_TOKEN` behält seine serverweiten Versandberechtigungen; persönliche Schlüssel erlauben ausschließlich Versand an das eigene Konto und dessen Geräte (keine Topics, Broadcasts oder fremden Benutzer).

### An Topic senden

```bash
BASE=https://push.example.com
curl --fail-with-body -sS "$BASE/api/send" \
  -H "Authorization: Bearer $SEND_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: backup-run-2026-11-07-1' \
  -d '{"topic":"backup","title":"Backup erfolgreich","body":"Alle Sicherungen abgeschlossen","longText":"Details: 32 Dateien erfolgreich geprüft.\nKeine Warnungen."}'
```

### An einen Benutzer senden

```bash
curl --fail-with-body -sS "$BASE/api/send" \
  -H "Authorization: Bearer $SEND_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"userId":"<USER_ID>","title":"Für dich","body":"Eine persönliche Nachricht"}'
```

Benutzer-IDs werden im Admin-Webinterface als Empfängerauswahl angeboten oder über `GET /api/admin/users` mit `ADMIN_TOKEN` ausgelesen. Eine gerätespezifische Nachricht lässt sich mit `deviceId` statt `userId` auslösen. Bei unbekanntem Ziel: HTTP 404; fehlendem Token: 401; ungültigem JSON: 400.

Beispielantwort:

```json
{
  "accepted": 0,
  "failed": 0,
  "expired": 0,
  "recipients": 2,
  "queued": 2,
  "inboxUsers": 1,
  "messageId": "<SERVER_GENERATED_ID>",
  "statusUrl": "/api/send/status/<SERVER_GENERATED_ID>"
}
```

`inboxUsers` ist die Anzahl der individuellen Benutzer-Posteingänge, `queued` die Anzahl dauerhaft eingeplanter Geräte-Zustellungen. **HTTP 200 bedeutet jetzt nur erfolgreiches Speichern in der Queue**, nicht zugestellte oder vom Push-Anbieter angenommene Benachrichtigungen. Die Felder `accepted`, `failed`, `expired` sind aus Gründen der API-Kompatibilität vorhanden, beim Einreihen aber immer 0. Der tatsächliche Status wird separat gelesen. Ein Topic ohne Abonnenten hat `queued: 0`.

**Langtexte:** Das optionale Feld `longText` nimmt bis zu **256 KiB UTF-8** auf. Der kurze `body`-Text wird als verschlüsselte Push-Nutzlast an Browserdienste gesendet, der `longText` verbleibt ausschließlich in SQLite und ist in der Benutzer-Inbox unter „Vollständige Nachricht anzeigen“ abrufbar. Ein Klick auf eine Systembenachrichtigung öffnet die Web-App an der zugehörigen Nachricht. Die Admin-Weboberfläche besitzt dafür ein eigenes Langtext-Feld. Das Feld wird nicht als HTML interpretiert.

**Zustellstatus mit API-Token prüfen:**

```bash
curl --fail-with-body -sS "$BASE/api/send/status/<MESSAGE_ID>" \
  -H "Authorization: Bearer $SEND_TOKEN"
```

Der Status enthält je Gerät `status`, `attempts`, `nextAttemptAt`, `acceptedAt`, `confirmedAt`, `lastError`. Das Browser-Empfangsgeheimnis wird nicht über die API ausgegeben.

**Idempotenz für externe Aufrufer:** `Idempotency-Key` ist optional, aber bei automatisierten Jobs empfohlen. Bei Wiederholung mit demselben Schlüssel und identischem Inhalt (innerhalb von 7 Tagen) liefert die API dieselbe `messageId` und fügt **keine** weiteren Inbox- oder Queue-Einträge hinzu. Ein anderer Inhalt bei demselben Schlüssel wird mit HTTP 409 abgewiesen. Schlüssel sollten pro logischer Nachricht eindeutig sein; Beispiel: `backup-2026-11-07-job42`.

## 6. Remote CLI (`pushiectl`)

**Persönlichen Schlüssel erstellen:** Per Passkey anmelden, **Konto → API-Schlüssel** öffnen, einen Namen vergeben und **API-Schlüssel erstellen** wählen. Den vollständigen Schlüssel sofort kopieren: Er wird nur einmal angezeigt und ausschließlich als SHA-256-Hash in der SQLite-Benutzerzeile gespeichert. Bis zu 20 Schlüssel pro Konto sind möglich. **Widerrufen** sperrt einen Schlüssel für neue API-Aufrufe; bereits gespeicherte Nachrichten werden weiter zugestellt. Schlüssel bleiben über Neustarts gültig, bis sie widerrufen werden.

```bash
export PUSH_BASE_URL='https://push.example.com'
export PUSH_API_TOKEN='<persönlicher API-Schlüssel>'
./pushiectl -title 'Backup' -body 'Fertig'
./pushiectl -device '<EIGENE_DEVICE_ID>' -title 'Test' -body 'Hallo'
./pushiectl -status '<MESSAGE_ID>'
```

Ohne Ziel verwendet ein persönlicher Schlüssel das eigene Konto. `-user` darf nur die eigene Benutzer-ID enthalten; `-device` muss zum eigenen Konto gehören. Statusabfragen sind nur für mit persönlichen Schlüsseln desselben Kontos gesendete Nachrichten möglich. Retry-Schlüssel (`-key`) sind pro Konto getrennt. Die CLI-Option `-key` ist ein Idempotency-Key, kein Zugangsschlüssel. Verwende `PUSH_API_TOKEN` für den Zugangsschlüssel.

Die CLI liegt unter `src/cmd/pushiectl` und verwendet **nur die Go-Standardbibliothek**. Einmal bauen und die Binärdatei auf einen beliebigen anderen Server kopieren:

```bash
mkdir -p bin
(cd src && go build -o ../bin/pushiectl ./cmd/pushiectl)

# Auf dem Remote-Server: URL und API-Token als Umgebungsvariablen konfigurieren
export PUSH_BASE_URL=https://push.example.com
export PUSH_API_TOKEN='dein_SEND_TOKEN'

# Topic-Nachricht
./pushiectl -topic backup -title 'Backup' -body 'Fertig' -key 'backup-run-2026-11-07-1'

# Benutzer-Nachricht
./pushiectl -user '<USER_ID>' -title 'Wartung' -body 'Server wird neugestartet'

# Über stdin aus einem anderen Skript
printf 'Disk usage is high' | ./pushiectl -topic system -title 'Alarm' -stdin

# Push-Status einer vorherigen Nachricht abfragen:
./pushiectl -status '<MESSAGE_ID>'
```

`pushiectl -device '<DEVICE_ID>'` ist ebenfalls möglich. Die CLI hat standardmäßig ein HTTP-Timeout von 60 Sekunden, lehnt Redirects ab und verwendet ausschließlich HTTPS. Nutze bevorzugt `PUSH_API_TOKEN` statt `-token`, damit der Token nicht in Shell-Prozesslisten auftaucht.

Zum Mitsenden eines ausführlichen Logs als Langtext:

```bash
./pushiectl -topic backup -title 'Backup' -body 'Details in der Web-App' -longtext-file ./backup.log
```

## 7. Persönliche Nachrichten-Inbox

Im Web-Interface unter **Meine Nachrichten** sieht jeder angemeldete Benutzer nur seinen eigenen Posteingang:

- Titel, Text, Topic/Direct-Kennzeichnung und Zeitstempel.
- Pro Gerät der Zustellstatus, Anzahl Versandversuche sowie bei Bedarf der nächste Wiederholungszeitpunkt.
- Ungelesene Nachrichten werden hervorgehoben und gezählt.
- **Gelesen**, **Alle gelesen**, **Löschen**, **Aktualisieren**.
- Als gelesen markierte Nachrichten schließen beim nächsten Inbox-Abruf auch die zugehörigen Systembenachrichtigungen auf anderen Geräten desselben Benutzers. Sichtbare App-Fenster aktualisieren alle 30 Sekunden und beim Zurückkehren zur App. Bei geschlossener App erfolgt der Abgleich erst beim nächsten Öffnen; die Nachricht bleibt im Posteingang erhalten.
- Automatische Aktualisierung, solange die Seite sichtbar ist (alle 30 Sekunden sowie beim Zurückkehren in die Seite).
- **Persistenz:** pro Benutzer maximal die letzten 200 Nachrichten in SQLite; ältere werden beim nächsten Eingang inklusive Langtext verdrängt.

Eine Nachricht wird **vor** dem Push-Versand in der Inbox gespeichert. Sie bleibt dort auch dann, wenn kein Gerät registriert ist oder der Push-Anbieter einen Fehler meldet. Löschen und Lesestatus sind **benutzerbezogen**; eine Änderung wirkt nicht auf andere Empfänger derselben Topic-Nachricht.

### HTTP-Endpunkte (Auswahl)

| Methode | Pfad | Authentifizierung |
|---|---|---|
| `GET` | `/api/config` | öffentlich: nur VAPID-Public-Key und Bootstrap-Verfügbarkeit |
| `POST` | `/api/auth/bootstrap/begin`, `/api/auth/bootstrap/finish` | einmaliger `BOOTSTRAP_OTP`, danach WebAuthn |
| `POST` | `/api/auth/register/begin`, `/api/auth/register/finish` | einmalige Einladung, danach WebAuthn |
| `POST` | `/api/auth/login/begin`, `/api/auth/login/finish` | WebAuthn |
| `GET` | `/api/me` | Benutzer-Session |
| `GET` | `/api/me/messages` | Benutzer-Session |
| `PUT` | `/api/me/messages/{id}/read` | Benutzer-Session + gleiche Origin |
| `POST` | `/api/me/messages/read-all` | Benutzer-Session + gleiche Origin |
| `DELETE` | `/api/me/messages/{id}` | Benutzer-Session + gleiche Origin |
| `PUT` / `DELETE` | `/api/me/topics/{topic}` | Benutzer-Session + gleiche Origin |
| `POST` / `DELETE` | `/api/me/devices` / `/api/me/devices/{id}` | Benutzer-Session + gleiche Origin |
| `GET` | `/api/admin/targets` | Admin-Session |
| `POST` | `/api/admin/send` | Admin-Session + gleiche Origin |
| `POST` | `/api/admin/invites/web`, `/api/admin/topics/web` | Admin-Session + gleiche Origin |
| `POST` | `/api/send` | `SEND_TOKEN` oder persönlicher Bearer-Schlüssel; optional `Idempotency-Key` |
| `GET` | `/api/send/status/{id}` | `SEND_TOKEN` oder persönlicher Bearer-Schlüssel (eigene Sends) |
| `GET` | `/api/me/keys` | Benutzer-Session; nur Schlüssel-Metadaten |
| `POST` | `/api/me/keys` | Benutzer-Session + gleiche Origin; JSON `{"name":"CLI"}` |
| `DELETE` | `/api/me/keys/{id}` | Benutzer-Session + gleiche Origin |
| `GET` | `/api/admin/deliveries/{id}` | Admin-Session |
| `POST` | `/api/push/ack` | einmaliges, mit der Payload übermitteltes Geräte-Bestätigungsgeheimnis |
| `POST` | `/api/admin/invites`, `/api/admin/topics` | `ADMIN_TOKEN` Bearer |
| `GET` | `/api/admin/users` | `ADMIN_TOKEN` Bearer |
| `POST` | `/api/admin/users/{id}/promote` | `ADMIN_TOKEN` Bearer (Migration) |
| `GET` | `/api/admin/storage/stats` | Admin-Session |
| `POST` | `/api/admin/storage/preview`, `/api/admin/storage/cleanup`, `/api/admin/storage/compact` | Admin-Session + gleiche Origin |

## Sicherheit und Betrieb

- Die Browser-API nutzt `HttpOnly`, `SameSite=Strict`, bei HTTPS `Secure`-Cookies, eine **exakte Origin-Prüfung** für alle schreibenden Sitzungs-Endpunkte, WebAuthn-Challenges und HTTPS.
- Zugangstokens sind getrennt für **Versand** und **Administration**. Das Admin-Webinterface verwendet stattdessen das angemeldete Admin-Konto.
- Push-Endpunkte werden serverseitig auf bekannte Browser-Push-Domains beschränkt, um SSRF über manipulierte PushSubscriptions zu verhindern.
- Die Inbox-Texte, die Browser-Endpunkte/Schlüssel und die gerätegebundenen ACK-Secrets liegen in der SQLite-Datenbank im Klartext. Das Dateisystem wird mit restriktiven Rechten (`0600`) beschrieben. Auf dem Host Schutz und Backups des Volumes einrichten.
- **Versandstatus:** Provider-HTTP-2xx beendet den Versand erfolgreich als `accepted`, ohne auf eine Gerätebestätigung zu warten. Dies bestätigt die Annahme beim Push-Dienst. Eine freiwillige Browserbestätigung kann weiterhin als `confirmed` protokolliert werden.
- **Retries:** Worker prüft standardmäßig alle 5 Sekunden; bei Netzwerkfehlern/HTTP 408, 425, 429, 5xx: exponentieller Backoff ab 30 Sekunden bis 30 Minuten, unter Beachtung von `Retry-After`; Bei Provider-2xx wird nicht erneut versendet. Es gibt keine Wartefrist für Browser-ACKs. Bereits gespeicherte `unconfirmed`-Aufträge mit Providerannahme werden beim Neustart zu `accepted`, und alte Bestätigungsfristen werden entfernt. Maximal 8 Versuche und höchstens 24 Stunden ab Einreihen. HTTP 404/410 beendet die Zustellung und entfernt das veraltete Gerät. Permanente 4xx werden nicht erneut versucht. Web-Push-Payload-TTL beim Anbieter: 3600 Sekunden.
- **Doppelte Browseranzeigen sind trotz Retry-Deduplizierung nicht völlig auszuschließen.** Der Service Worker setzt die Nachrichten-ID als Notification-Tag, um mehrfach zugestellte Meldungen möglichst zu ersetzen. Externe API-Retries sollten zusätzlich denselben Idempotency-Key verwenden.
- **Betrieb:** SQLite ist der persistente Speicher; der Dienst hält weiterhin einen In-Memory-Lesecache und unterstützt deshalb **nur eine Serverinstanz pro DB-Datei**. Für mehrere Instanzen wären koordiniertes Job-Leasing und ein geeigneter Queue-Backend nötig.
- Die Projektgröße eignet sich für private Geräte, Familien und kleine Homelabs. Für hohe Last/Multi-Instance wären SQLite/PostgreSQL mit atomaren Job-Leases, Rollen- und Auditfunktionen sinnvoll.
- **Keine echte Ende-zu-Ende-Verschlüsselung des Inbox-Speichers:** Die Web-Push-Payload wird für den Transport zu Browser-Push-Diensten verschlüsselt, aber die serverseitige Inbox ist bewusst persistent lesbar.

## Lokale Tests

```bash
cd src
go test ./...
go test -race ./...
go vet ./...
```

Enthalten sind Tests für Retry, Browser-ACK, persistente Zustände, ausgefallene Geräte, Statusrechte, Idempotenz und Admin-Datenbankbereinigung mit Vorschau, Berechtigungsprüfung, Neustartpersistenz sowie VACUUM. In dieser Umgebung konnten die Go-Tests nicht ausgeführt werden. Außerdem: auf einem echten iPhone/iPad einen Passkey registrieren, die PWA vom Home-Bildschirm öffnen, Push erlauben und eine Topic- und Direktnachricht senden. Gleiches für macOS testen. Auf dem externen Server die API und die CLI überprüfen.

### Tabs und Empfänger
Die Oberfläche startet mit Nachrichten. Administratoren öffnen den Versand über „Senden“ in einem modalen Fenster. Ein einziges Menü-Icon in der Kopfzeile bietet Nachrichten, Topics, Konto, Administration (nur Admins), Sprache und Abmelden. Die Topic-Ansicht vereint Abos für alle Benutzer und Anlegen/Löschen für Administratoren, einschließlich der Standard-Topics system, backup und automation. Gelöschte Topics und deren Abos bleiben auch nach einem Neustart gelöscht; bestehende Nachrichten bleiben erhalten. Unter Administration können Admins andere Benutzer befördern oder mit zugehörigen Kontodaten löschen; das eigene Konto ist vor Löschung geschützt. Dort befinden sich auch Datenbankstatistik und Wartung.
Beim Versand wählt man ein Topic, einen Benutzer (alle oder ausgewählte Geräte) oder alle Benutzer. Die API unterstützt zusätzlich `{"userId":"…","deviceIds":["…"]}` oder `{"broadcast":true}` zusammen mit Titel und Nachricht. Broadcast erreicht alle bestehenden Benutzer einschließlich Administratoren; Benutzer ohne Geräte erhalten einen Inbox-Eintrag.

### Sprache / Language
Die Oberfläche unterstützt Deutsch und Englisch. Unter **Konto → Sprache** wird `de` oder `en` im Benutzerkonto gespeichert und nach der Anmeldung auf allen Geräten angewendet. Vor der Anmeldung gilt die lokale Sprachwahl, andernfalls die Browsersprache (Deutsch bei `de`, sonst Englisch). Der Sprachwechsel lädt die Seite neu. Namen, Topics und Nachrichteninhalte werden nicht übersetzt. Der Bestätigungstext für Datenbankbereinigung bleibt in beiden Sprachen `LÖSCHEN`.

`GET /api/me` enthält `language` (bei bestehenden Konten zunächst leer). `PUT /api/me/settings` mit `{"language":"de"}` oder `{"language":"en"}` speichert die Sprache des angemeldeten Benutzers; dieselben Sitzungs- und Origin-Prüfungen wie bei anderen Kontoänderungen gelten. Keine Schema-Migration erforderlich.

### Erscheinungsbild / Appearance
Unter **Konto → Erscheinungsbild** stehen Hell, Dunkel und Automatisch zur Auswahl. Automatisch (Standard) folgt der Systemeinstellung, auch wenn sie sich bei geöffneter App ändert. Die Auswahl wird im Benutzerkonto und lokal für den nächsten Seitenaufruf gespeichert. `PUT /api/me/settings` akzeptiert zusätzlich `{"theme":"auto"}`, `{"theme":"light"}` oder `{"theme":"dark"}`. Sprache und Design können unabhängig oder gemeinsam aktualisiert werden; ausgelassene Felder bleiben unverändert. `GET /api/me` enthält `theme`; bestehende Konten verwenden automatisch die Systemeinstellung.

### Security hardening (2026-11-08)

- Compose now binds **127.0.0.1:8099**. A reverse proxy on the host can keep using this address. A proxy in another container must use a private shared Docker network and the service address `push:8099`; do not republish the raw HTTP port publicly.
- Set `TRUSTED_PROXIES` to a comma-separated list of the **actual proxy CIDRs**, as seen by the application. It defaults to empty. The proxy must overwrite or append `X-Forwarded-For`. Never trust client-supplied forwarding headers or configure all addresses as trusted. Separate budgets apply to authentication (30/minute per client), reads (120), writes (90), and push receipts (600), with global ceilings; account reads/writes are also limited. Limits reset after a process restart.
- Each user can register at most **20 devices**. Existing devices may still refresh at the limit. Existing over-limit accounts are retained but cannot add devices. New sends are rejected atomically if they would exceed **1,000 active deliveries per user** or **10,000 globally**.
- The serialized in-memory state has a **128 MiB growth limit**. Existing larger databases can still open; shrinking/non-growing updates remain possible. This is a guardrail for the current single-process cache, not a disk quota or a replacement for scoped SQL writes. Inboxes still retain at most 200 messages per user.
- The production entrypoint acquires an OS lock beside the database (`.lock`) before opening it. A second server exits immediately. Leave the lock file in place; a stopped process automatically releases the kernel lock. Deploy one server per local SQLite database; network-filesystem or multi-replica deployment is unsupported.
- Delivery batches query SQLite for the earliest 100 due jobs and run at most **four concurrent requests**. SIGTERM cancels the worker and drains HTTP requests before closing storage. Changed subscriptions survive stale provider responses and retry using their new credentials.
- Logout/identity changes clear cached messages, invitation codes, drafts, and maintenance previews. Responses from a previous identity are discarded.
- Keep `.env` owner-readable only (`chmod 600 .env`). Check permissions and previous local access on your deployment before deciding whether token rotation is needed.

Validation commands: `go test -race ./...`, `go vet ./...`, and `node --test src/tests/*.cjs`. The application still uses a complete cache for writes; a future SQL-first redesign is needed for larger installations. Account disable/credential management, expanded observability, and dependency/container vulnerability scans remain separate follow-up work.

### Nachrichtenlog steuern und bereinigen

Unter **Administration → Zentrales Nachrichtenprotokoll** kann das Schreiben des Nachrichtenlogs ein- oder ausgeschaltet werden. Die Einstellung wird dauerhaft gespeichert; standardmäßig ist das Log eingeschaltet. Im ausgeschalteten Zustand werden weder neue Logeinträge noch Aktualisierungen vorhandener Logeinträge geschrieben. Nachrichten in den persönlichen Posteingängen, die Versandqueue und technische Serverlogs arbeiten weiter.

Die separate Logbereinigung löscht Einträge älter als die angegebene Zahl von Tagen; **0** wählt alle Logeinträge. Zuerst die Vorschau aufrufen, danach die Löschung bestätigen. Die Bereinigung löscht keine Posteingangsnachrichten oder Zustellaufträge. Gelöschte Logeinträge werden beim Neustart nicht wiederhergestellt. Ausschalten allein löscht keine vorhandenen Einträge.

Admin-API (Session und gleiche Origin erforderlich): `GET/PUT /api/admin/message-log/settings` mit `{"enabled":true|false}`, `POST /api/admin/message-log/cleanup-preview` mit `{"days":30}`, und `POST /api/admin/message-log/cleanup` mit `{"days":30,"confirm":"LÖSCHEN"}`. Die Bereinigungsantwort enthält `entries`.
