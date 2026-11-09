# Watchtower-Benachrichtigungen

Watchtower nutzt [Shoutrrr](https://watchtower.nickfedor.com/v1.24.0/notifications/introduction/). Pushie nimmt dessen [Generic-JSON-Template](https://pkg.go.dev/github.com/nicholas-fedor/shoutrrr/pkg/services/specialized/generic) an:

```http
POST /api/integrations/watchtower
Authorization: Bearer <Integrationsschlüssel oder persönlicher API-Schlüssel>
Content-Type: application/json

{"title":"Watchtower","message":"Container aktualisiert"}
```

## Schlüssel anlegen

Als Admin im Menü **Administration → Integrationsschlüssel** einen Namen (z. B. Watchtower) vergeben und den Schlüssel erstellen. Er wird nur einmal angezeigt; anschließend in die Watchtower-Secret-Datei übernehmen. Alle Admins können diese unabhängigen Schlüssel auflisten und widerrufen. Sie gehören keinem persönlichen Konto und erlauben nur den Watchtower-Webhook, nicht die allgemeine Versand- oder Administrations-API.

Der globale `SEND_TOKEN` wird an diesem Endpoint nicht mehr akzeptiert. Bestehende Watchtower-Konfigurationen müssen auf einen Integrationsschlüssel oder einen persönlichen API-Schlüssel umgestellt werden. Persönliche API-Schlüssel bleiben auf das eigene Konto beschränkt.

## Empfänger und Inhalt

- Mit einem Integrationsschlüssel: standardmäßig Topic `automation`. Im Menü unter Topics abonnieren; ein zuvor gelöschtes Topic wieder anlegen.
- Alternativ genau ein Ziel als Query-Parameter setzen: `topic`, `userId` oder `deviceId`.
- Mit einem persönlichen API-Schlüssel aus Konto → API-Schlüssel: ohne Zielangabe nur das eigene Konto. Topic-Versand und fremde Empfänger bleiben gesperrt.
- Leerer Titel wird zu `Watchtower`. Meldungen bis 256 KiB werden angenommen und immer vollständig als Langtext gespeichert. Ab 2001 Bytes erhält die Push-Nachricht eine gekürzte Vorschau.
- Erfolg (`200`) bedeutet Speicherung und Einreihung in die persistente Zustellungsqueue, keine Empfangsgarantie. Antwort und optionaler `Idempotency-Key` entsprechen `/api/send`.
- Authentifizierung erfolgt ausschließlich per Bearer-Header; Browser-Sessions und Admin-Token reichen nicht aus.

## Watchtower konfigurieren

Eine Datei `secrets/watchtower-notification-url.txt` im Hauptverzeichnis anlegen. Sie enthält genau eine Zeile; `DEIN_INTEGRATIONSSCHLUESSEL` und die Domain ersetzen:

```text
generic://push.example.com/api/integrations/watchtower?template=json&title=Watchtower&@Authorization=Bearer%20DEIN_INTEGRATIONSSCHLUESSEL
```

Für ein anderes Topic `&topic=system` anhängen. Persönliche Schlüssel benötigen keinen Topic-Parameter. Token mit URL-Sonderzeichen müssen im Header-Querywert URL-kodiert werden. Die erzeugten Integrationsschlüssel verwenden URL-sichere Zeichen.

In die eigene Compose-Konfiguration für Watchtower übernehmen:

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

Die Datei enthält Zugangsdaten und wird durch `.gitignore` und `.dockerignore` ausgeschlossen. `generic://` verwendet HTTPS. Die URL mit `template=json` sorgt für korrektes JSON-Escaping auch bei mehrzeiligen Berichten und Anführungszeichen. Die Integration benötigt keine zusätzlichen Pushie-Umgebungsvariablen.

## Direkt testen

```bash
curl --fail-with-body https://push.example.com/api/integrations/watchtower \
  -H "Authorization: Bearer ${INTEGRATION_KEY}" \
  -H 'Content-Type: application/json' \
  --data '{"title":"Watchtower-Test","message":"Die Anbindung funktioniert."}'
```

Danach den Posteingang eines Abonnenten prüfen. Watchtower sendet standardmäßig auch beim Start eine Meldung. Ein Live-Test mit Watchtower und einem registrierten Push-Gerät ist separat erforderlich.
