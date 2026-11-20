# STACKIT Landing Zone (Terraform-Beispiel)

Praxisnahes Beispiel einer Landing Zone, das komplett gegen den lokalen Mock läuft (und mit minimalen Änderungen gegen die echte STACKIT-Cloud).

| Zone | Inhalt |
|---|---|
| **PLZ** – Platform Landing Zone | Projekt `lz-dev-platform`, **Network Area** (Region `eu01`, Adressraum `10.100.0.0/16`, Transfer-Netz `10.255.0.0/24`, Default-Nameserver), **managed DNS** (Zone `dev.lz.example.com`) |
| **ALZ 1** – `lz-dev-k8s` | geroutetes Netz `10.100.1.0/24` und ein **SKE-Cluster** (3 Nodes, 3 Availability Zones) |
| **ALZ 2** – `lz-dev-vms` | eigenes geroutetes Netz `10.100.2.0/24`, **3 VMs** (je Boot- und Daten-Volume, je eine Zone, Anti-Affinity) für einen selbst betriebenen Cluster, Security Group, Key Pair, Public IP an Node 1 |

Ordnerstruktur in der Organisation: `platform/` (PLZ) und `applications/` (beide ALZ). Das Diagramm steht in [architecture.md](architecture.md).

## Aufbau

```
docs/landing-zone/
  versions.tf providers.tf variables.tf main.tf outputs.tf terraform.tfvars.example
  modules/
    platform-lz/   Projekt, Network Area + Region, DNS-Zone
    alz-k8s/       Projekt (Label networkArea), Netz, SKE-Cluster
    alz-vms/       Projekt (Label networkArea), Netz, Security Group, Affinity Group, Key Pair, 3 Server + Volumes, Public IP
```

Wie die Teile zusammenhängen:

- **Projekt → Network Area:** Es gibt keinen Attach-Aufruf. Ein Projekt wird über das Label `networkArea = <area-id>` beim Anlegen an die Area gehängt (danach nicht mehr änderbar). Deshalb hängen beide ALZ-Module von `module.platform.network_area_id` ab, das erst nach der Area-Region (Ranges, Transfer-Netz) bereitsteht.
- **Gerouteter Adressraum:** Die Netze der ALZ sind `routed = true` und werden aus der Area-Range `10.100.0.0/16` geschnitten. Die Default-Nameserver der Area bekommen sie per DHCP.
- **Cluster-Verkehr der VMs:** Die Security Group hat eine Regel mit sich selbst als Quelle (etcd, kubelet, CNI usw. nur zwischen den Nodes). SSH und die Kubernetes-API (6443) sind nur aus `admin_cidr` erreichbar. Die drei Server haben eine Anti-Affinity-Group und liegen in drei Zonen.
- **DNS:** Die Records (`node-N.vms.<zone>`, `api.vms.<zone>`) liegen im Root-Modul, weil sie die Zone der PLZ und die IPs der ALZ brauchen. In den Modulen gäbe das einen Zyklus.

## Ausführen gegen den Mock

```bash
# Terminal 1: Mock mit TUI (Default-Port 9001)
./bin/stackit_mock

# Terminal 2
cd docs/landing-zone
terraform init
terraform apply                      # mock_url Default: http://localhost:9001
terraform plan                       # erwartet: No changes
terraform destroy
```

Anderer Port: `terraform apply -var mock_url=http://localhost:18080`. Eigene Werte (Organisation, Namen, Netze, SSH-Key, Admin-CIDR): `cp terraform.tfvars.example terraform.tfvars`.

Während `apply` läuft, zeigt die TUI die Projekte, Folder, Area, DNS-Zone, Server und den Cluster. Mit `scripts/mock_stackit` kann man dasselbe über das echte `stackit`-CLI abfragen, z. B. `scripts/mock_stackit server list --project-id <id> --region eu01` (Projekt-IDs stehen in `terraform output projects`).

**Dauer:** Der Mock antwortet sofort. Die Wartezeit sind feste Sleeps im Provider (Projekt 60 s, Folder 10 s, SKE-Service-Enablement 15 s); die drei Projekte werden parallel angelegt. Gemessen gegen den Mock: `apply` ≈ 90 s für 35 Ressourcen, `plan` danach „No changes“, `destroy` wenige Sekunden.

## Gegen die echte STACKIT-Cloud

Die `*_custom_endpoint`-Zeilen in `providers.tf` entfernen, statt `service_account_token` ein `service_account_key_path` setzen und `organization_id`, `owner_email`, die DNS-Zone (eine Domain, die Sie delegieren können) und `ssh_public_key` anpassen. Vor dem ersten echten `apply` unbedingt `terraform plan` prüfen: Es entstehen kostenpflichtige Ressourcen.

## Grenzen

- Der Provider kann ein bestehendes Projekt **nicht nachträglich** an eine Network Area hängen (nur per Label beim Anlegen). Eine **private** DNS-Zone je Area gibt es im Provider nicht, die Zone ist öffentlich.
- Routing Tables und die Alpha-VPC-Ressourcen sind nicht enthalten.
- Der Mock löscht Projekte und Folder sofort, die echte Cloud verzögert das (Projekte bleiben bis zu 7 Tage „unsichtbar“; das kann das Löschen des übergeordneten Folders blockieren).
- Die Server verwenden im Mock ein statisches Image (`a0000000-0000-4000-8000-000000000001`). In der echten Cloud eine Image-ID (z. B. Ubuntu/Flatcar) per `image_id`-Variable setzen.
