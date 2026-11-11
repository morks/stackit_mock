# stackit_mockup

> Inoffizielles Community-Projekt, nicht mit STACKIT / Schwarz IT verbunden. Der Mock bildet nur das beobachtete API-Verhalten nach und ersetzt keine Tests gegen die echte Cloud.

Go-Mock der STACKIT-REST-APIs, damit Terraform-Code (Provider `stackitcloud/stackit`) und das `stackit`-CLI **ohne echte Cloud und ohne Kosten** getestet werden können. Der Mock läuft zusammen mit einer TUI, die live zeigt, was „deployt“ ist.

Unterstützte Services: Resource Manager, Service Account, IaaS (Netzwerke, Security Groups, Server, Volumes, Public IPs, NICs, Key Pairs, Images, Machine Types), SKE (+ Service Enablement), PostgreSQL Flex, MongoDB Flex.
Nicht abgedeckt: Network-Area-Routen/-Ranges, Backups/Snapshots/Clone, IaaS-Beta-Ressourcen (siehe `docs/api-notes.md`).

## Wie es funktioniert

```
Terraform / stackit-CLI ──HTTP──▶ stackit_mock (:9001) ──▶ In-Memory-Store ◀── TUI (Admin-API)
                                   │   /iaas /ske /resourcemanager /serviceaccount
                                   │   /postgresflex /mongodbflex /serviceenablement /token
                                   └── /_admin/* (Ressourcen, Events, Reset, Fehlerinjektion)
```

- **Ein Binary** `stackit_mock`: HTTP-Server (nur HTTP, TLS gehört in einen Reverse Proxy) und TUI.
- **State** liegt im Speicher; optional als JSON-Snapshot (`-snapshot`), der beim Start geladen und alle 2 s sowie beim Beenden geschrieben wird.
- **Pfad-Präfix pro Service** (`/iaas`, `/ske`, …), weil `GET /v2/projects/{id}` sonst zwischen Resource Manager und IaaS kollidiert. Der Provider/das CLI bekommt je Service einen Custom-Endpoint `http://host:9001/<svc>`.
- **Async-Zustände**: Ressourcen wechseln `CREATING → ACTIVE/CREATED` bzw. `DELETING → weg`. Standard ist sofort (`-delay 0`); mit `-delay 2s` sieht man die Übergänge in der TUI.
- **Antworten** enthalten alle Pflichtfelder der SDK-Modelle, sonst scheitert der Decode im Provider.
- **Auth**: `/token` liefert ein JWT-förmiges, unsigniertes Token mit `exp`. Jede Eingabe wird akzeptiert (es sei denn `MOCK_STRICT_AUTH=1`).
- **Fehlerinjektion** über `/_admin/faults` (z. B. 503 für die nächsten N Requests).

## Bauen und starten

Voraussetzung: Go (siehe `go.mod`).

```bash
git clone git@github.com:morks/stackit_mock.git && cd stackit_mock
go build -o bin/stackit_mock ./cmd/stackit_mock

./bin/stackit_mock                        # Server + TUI (Port 9001)
./bin/stackit_mock -addr :19001           # anderer Port
./bin/stackit_mock -headless              # nur Server, Request-Log auf stderr (CI, Container)
./bin/stackit_mock -tui-only -url http://localhost:9001/_admin   # nur TUI gegen laufenden Mock
./bin/stackit_mock -snapshot state.json   # State übers Neustarten behalten
./bin/stackit_mock -delay 2s              # Statusübergänge sichtbar machen
```

| Flag | Env | Default | Bedeutung |
|---|---|---|---|
| `-addr` | `MOCK_ADDR` | `:9001` | Listen-Adresse |
| `-base-url` | `MOCK_BASE_URL` | aus Request | Basis-URL für Links in Antworten (hinter Proxy; `X-Forwarded-*` wird beachtet) |
| `-snapshot` | `MOCK_SNAPSHOT` | leer | JSON-Snapshot-Datei |
| `-delay` | `MOCK_DELAY` | `0s` | Dauer asynchroner Statusübergänge |
| – | `MOCK_STRICT_AUTH=1` | aus | Requests ohne `Authorization` ablehnen |
| `-headless` | | | nur Server |
| `-tui-only`, `-url` | `MOCK_ADMIN_URL` | `http://localhost:9001/_admin` | nur TUI |
| `-quiet` | | | Request-Log aus |

**Hinweis zum Port:** Tools wie Rancher Desktop belegen z. B. `127.0.0.1:8080` per SSH-Forward. Dann landen Requests an `localhost:8080` dort statt beim Mock (typisch: HTML-Antwort, `invalid character '<'`). Der Default ist deshalb `9001`; bei Konflikten einen anderen Port nehmen (`-addr :19001`) und diesen überall angeben. Im kombinierten Modus greift die TUI ohne Netzwerk direkt auf den Mock zu und ist davon nicht betroffen.

### TUI

Die Kopfzeile zeigt die Mock-URL mit Port (`mock URL: http://localhost:9001`). Baum Projekt → Service → Ressource, rechts Detail (JSON), unten Event-Log.

| Taste | Aktion |
|---|---|
| `↑`/`↓`, `k`/`j` | navigieren |
| `/` | Filter (Typ, Name, ID, Status); `enter` übernimmt, `esc` löscht |
| `r` | alles zurücksetzen (mit `y` bestätigen) |
| `q` | beenden (stoppt auch den Server, schreibt den Snapshot) |

bubbletea fragt beim Start die Terminalfarbe ab; Terminals ohne Antwort verzögern den Start um bis zu ~5 s. Alternative: `-headless` in einem Terminal und `-tui-only` im anderen.

## Admin-API

```bash
curl localhost:9001/_admin/health
curl localhost:9001/_admin/resources
curl 'localhost:9001/_admin/events?since=0'
curl -XPOST localhost:9001/_admin/reset
curl localhost:9001/_admin/faults
curl -XPOST localhost:9001/_admin/faults -d '{"method":"POST","path_prefix":"/iaas","status":503,"remaining":1}'
curl -XDELETE localhost:9001/_admin/faults            # alle; mit ?id=… einzeln
```

## Terraform

Beispiel: `docs/terraform-example/main.tf` (Projekt, Service Account, Netzwerk, Security Group + Regel, Public IP, Volume, Server + NIC, SKE, PostgreSQL Flex, MongoDB Flex).

```bash
./bin/stackit_mock -addr :19001 &           # oder in eigenem Terminal mit TUI
cd docs/terraform-example
terraform init
terraform apply -var mock_url=http://localhost:19001
terraform plan  -var mock_url=http://localhost:19001     # erwartet: No changes
terraform destroy -var mock_url=http://localhost:19001
```

Der Provider wird mit Custom-Endpoints auf den Mock gerichtet:

```hcl
provider "stackit" {
  default_region        = "eu01"
  service_account_token = "mock-token"

  resourcemanager_custom_endpoint    = "${var.mock_url}/resourcemanager"
  service_account_custom_endpoint    = "${var.mock_url}/serviceaccount"
  iaas_custom_endpoint               = "${var.mock_url}/iaas"
  ske_custom_endpoint                = "${var.mock_url}/ske"
  service_enablement_custom_endpoint = "${var.mock_url}/serviceenablement"
  postgresflex_custom_endpoint       = "${var.mock_url}/postgresflex"
  mongodbflex_custom_endpoint        = "${var.mock_url}/mongodbflex"
}
```

Das Token `mock-token` wird vom Provider als deprecated gemeldet (Warnung), funktioniert aber gegen den Mock.

**Dauer:** Der Mock antwortet sofort; die Wartezeit sind feste Sleeps im Provider. `apply` ≈ 77 s (Projekt 60 s, Postgres 15 s, Service Enablement 15 s, MongoDB 5 s, Service Account 5 s), `destroy` ≈ 30 s (MongoDB-Löschen 30 s). Wer Zeit sparen will, nutzt eine feste Projekt-UUID als `project_id` statt `stackit_resourcemanager_project` – der Mock akzeptiert beliebige Projekt-IDs.

Der Mock-State ist im Speicher: Nach einem Mock-Neustart ohne `-snapshot` passt der Terraform-State nicht mehr; dann `*.tfstate*` löschen.

## stackit-CLI gegen den Mock

Das echte `stackit`-CLI (getestet mit 0.76.0) lässt sich per Umgebungsvariablen auf den Mock umbiegen. Der Wrapper `scripts/mock_stackit` holt ein Token vom Mock, setzt die `STACKIT_*_CUSTOM_ENDPOINT`-Variablen und ruft `stackit` auf. Login und Konfiguration des CLI bleiben unberührt.

```bash
export MOCK_URL=http://localhost:19001          # Default: http://localhost:9001
P=11111111-1111-4111-8111-111111111111          # Projekt-ID muss eine UUID sein

scripts/mock_stackit network list --project-id $P --region eu01
scripts/mock_stackit server list --project-id $P --region eu01
scripts/mock_stackit ske cluster list --project-id $P --region eu01
scripts/mock_stackit postgresflex instance list --project-id $P --region eu01
scripts/mock_stackit mongodbflex instance list --project-id $P --region eu01
scripts/mock_stackit service-account list --project-id $P

# optional als Kommando im PATH
ln -s "$PWD/scripts/mock_stackit" /usr/local/bin/mock_stackit
```

Geprüft ist `network list` mit Daten; die übrigen `list`-Befehle laufen gegen einen leeren Mock fehlerfrei. Schreibende Befehle (`create`/`delete`) sind nicht systematisch getestet.

## Entwicklung

```bash
go build ./... && go vet ./... && gofmt -l .
go test -race ./...
```

Aufbau:

- `cmd/stackit_mock/` – Binary (Server + TUI)
- `internal/store/` – In-Memory-Store, Events, Snapshot
- `internal/lifecycle/` – Async-Engine
- `internal/crud/` – generische REST-Collection, auf der die Services aufsetzen
- `internal/httpx/` – JSON-/Fehler-Helfer (service-spezifische Fehlerformate)
- `internal/services/<svc>/` – ein Handler je Service, registriert in `all.go`
- `internal/admin/`, `internal/faults/`, `internal/server/` – Admin-API, Fehlerinjektion, Middleware
- `internal/tui/` – Bubble-Tea-TUI
- `docs/api-notes.md` – verifizierte Endpunkte, Pflichtfelder, Waiter-Semantik (vor Handler-Änderungen lesen)
- `scripts/mock_stackit` – CLI-Wrapper
