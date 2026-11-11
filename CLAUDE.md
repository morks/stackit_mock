# stackit_mockup

Go-basierter Mockup von STACKIT-APIs für lokale Entwicklung und Tests (Terraform-Provider `stackitcloud/stackit` ohne Kosten testen).

## Layout
- `cmd/stackit_mock/` – ein Binary: Mock-Server (HTTP, Admin-API, Snapshot) und TUI (Bubble Tea, pollt die Admin-API)
- `internal/store/` – generischer In-Memory-Store (Typ/ID/Projekt), Events, JSON-Snapshot
- `internal/lifecycle/` – Async-Engine (CREATING→ACTIVE, DELETING→weg) per Timer
- `internal/crud/` – generische REST-Collection (`Coll`), auf der alle Services aufsetzen
- `internal/httpx/` – JSON/Fehler-Helfer, service-spezifische Fehlerformate
- `internal/services/{auth,resourcemanager,serviceaccount,iaas,ske,postgresflex,mongodbflex}/` – Handler je Service (`all.go` registriert sie)
- `internal/admin/`, `internal/faults/` – `/_admin/*` und Fehlerinjektion
- `docs/api-notes.md` – verifizierte Endpunkte/Pflichtfelder/Waiter-Semantik aus SDK + Provider (vor Änderungen an Handlern lesen)
- `docs/terraform-example/` – `main.tf` gegen den Mock

## Befehle
- Bauen: `go build -o bin/stackit_mock ./cmd/stackit_mock`
- Starten (Server + TUI): `./bin/stackit_mock` bzw. `go run ./cmd/stackit_mock`
  - `-headless` nur Server (Request-Log auf stderr; für CI/Lab/Container), `-tui-only -url http://host:9001/_admin` nur TUI gegen einen laufenden Mock
  - Flags/Env: `-addr`/`MOCK_ADDR` :9001, `-base-url`/`MOCK_BASE_URL`, `-snapshot`/`MOCK_SNAPSHOT`, `-delay`/`MOCK_DELAY` 0 (sofort; z. B. `2s`, um Statusübergänge in der TUI zu sehen), `MOCK_STRICT_AUTH=1`, `-url`/`MOCK_ADMIN_URL`
  - TUI-Tasten: `/` Filter, `esc` Filter löschen, `r` Reset (mit Bestätigung), `q` Beenden (stoppt auch den Server, schreibt den Snapshot)
  - Mit TUI wird das Request-Log unterdrückt (würde den Bildschirm stören); bubbletea fragt beim Start die Terminalfarbe ab, Terminals ohne Antwort verzögern den Start bis ca. 5 s
- Tests: `go test ./...`
- Qualität: `gofmt -l .` und `go vet ./...`

## Konventionen
- Jeder Service liegt unter eigenem Pfad-Präfix (`/iaas`, `/ske`, `/resourcemanager`, …), weil `GET /v2/projects/{id}` sonst zwischen Resource Manager und IaaS kollidiert. Provider: `<svc>_custom_endpoint = "http://host:9001/<svc>"`.
- Responses müssen alle Pflichtfelder des SDK-Modells enthalten (sonst Decode-Fehler im Provider); Werte mit `_`-Präfix in `Data` sind intern und werden nicht ausgeliefert.
- Nach DELETE liefert GET 404 (Waiter); SKE-Delete-Waiter pollt die Liste.
- Admin-API: `GET /_admin/resources|events?since=N|faults|health`, `POST /_admin/reset|faults`, `DELETE /_admin/faults[?id=]`.
- Fehlerinjektion: `curl -XPOST :9001/_admin/faults -d '{"method":"POST","path_prefix":"/iaas","status":503,"remaining":1}'`.
- Nur HTTP; TLS terminiert ein Reverse Proxy (X-Forwarded-* wird für Base-URLs beachtet).

## Geschwindigkeit
- Mock-seitig ist alles sofort (`-delay 0`, Default). Verbleibende Zeit sind **feste Sleeps im Provider**, nicht beeinflussbar: Projekt anlegen 60 s, Service Enablement (SKE) 15 s, Postgres Flex anlegen 15 s, MongoDB anlegen 5 s und **löschen 30 s** (`mongodbflex/instance/resource.go`), Service Account 5 s, Netzwerk 2 s.
- Ergebnis `docs/terraform-example`: apply ≈ 77 s, destroy ≈ 30 s (vorher mehrere Minuten mit `-delay 2s`, weil der Provider nur alle 5 s pollt).
- Für Statusübergänge in der TUI sichtbar machen: `-delay 2s` (kostet pro asynchroner Ressource ≥ 5 s).
