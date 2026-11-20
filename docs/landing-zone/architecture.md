# Architektur

Die STACKIT Landing Zone gliedert sich in eine Organisation mit zwei Foldern: `platform`
(Platform Landing Zone, PLZ) und `applications` (zwei Application Landing Zones, ALZ).
Die PLZ stellt eine organisationsweite Network Area sowie die DNS-Zone bereit, an die sich
die Netze der ALZ `lz-dev-k8s` (SKE) und `lz-dev-vms` (3 selbst betriebene Cluster-Nodes)
per Routing anschließen.

```mermaid
%% STACKIT Landing Zone – Architekturübersicht
flowchart TB
    classDef projekt fill:#e8f4fd,stroke:#1a73e8,stroke-width:1px;
    classDef netz fill:#fef7e0,stroke:#f9a825,stroke-width:1px;
    classDef compute fill:#e6f4ea,stroke:#34a853,stroke-width:1px;
    classDef policy fill:#fce8e6,stroke:#ea4335,stroke-width:1px,stroke-dasharray: 3 3;

    subgraph Org["Organisation (UUID)"]
        NA["Network Area: lz-dev-platform-area<br/>Region eu01<br/>Range 10.100.0.0/16<br/>Transfer-Netz 10.255.0.0/24<br/>Default-Nameserver"]

        subgraph FolderPlatform["Folder: platform"]
            subgraph ProjPlatform["Projekt: lz-dev-platform (PLZ)<br/>modules/platform-lz"]
                DNSZone["DNS-Zone<br/>dev.lz.example.com"]
                DNSRecords["Record-Sets:<br/>node-1/2/3.vms A<br/>api.vms A"]
            end
        end

        subgraph FolderApps["Folder: applications"]
            subgraph ALZK8s["ALZ: lz-dev-k8s<br/>modules/alz-k8s<br/>Label networkArea"]
                NetK8s["Geroutetes Netz<br/>10.100.1.0/24"]
                SKE["SKE-Cluster"]
                NodePool["Node Pool<br/>(3 Availability Zones)"]
            end

            subgraph ALZVms["ALZ: lz-dev-vms<br/>modules/alz-vms<br/>Label networkArea"]
                NetVms["Geroutetes Netz<br/>10.100.2.0/24"]
                SG["Security Group: cluster<br/>Self-Regel + SSH/6443<br/>nur von Admin-CIDR"]
                AG["Affinity Group<br/>(hard-anti-affinity)"]
                KP["Key Pair"]
                Node1["Server node-1<br/>Boot-Vol + Daten-Vol (AZ1)<br/>+ Public IP"]
                Node2["Server node-2<br/>Boot-Vol + Daten-Vol (AZ2)"]
                Node3["Server node-3<br/>Boot-Vol + Daten-Vol (AZ3)"]
            end
        end
    end

    NA <-->|geroutet| NetK8s
    NA <-->|geroutet| NetVms

    NetK8s --> SKE --> NodePool

    NetVms --> Node1 & Node2 & Node3
    SG -.->|schützt| Node1 & Node2 & Node3
    AG -.->|hard-anti-affinity| Node1 & Node2 & Node3
    KP -.->|SSH-Key| Node1 & Node2 & Node3

    DNSRecords -->|A: node-1..3.vms| Node1 & Node2 & Node3
    DNSRecords -->|A: api.vms| Node1

    class ProjPlatform,ALZK8s,ALZVms projekt
    class NA,NetK8s,NetVms netz
    class SKE,NodePool,Node1,Node2,Node3 compute
    class SG,AG,KP policy
```

### Legende

- **Folder → Projekt**: Durch Verschachtelung dargestellt – `platform` enthält das Projekt
  `lz-dev-platform`, `applications` enthält die Projekte der beiden ALZ.
- **Network Area ↔ ALZ-Netze** (durchgezogene Doppelpfeile, `geroutet`): Die Network Area
  `lz-dev-platform-area` wird auf Organisationsebene verwaltet und per Label `networkArea`
  an beide Application Landing Zones angehängt.
- **DNS-Records** (durchgezogene Pfeile): zeigen auf die privaten IPs von `node-1`–`node-3`
  (`node-1/2/3.vms`) bzw. auf die Public IP von `node-1` (`api.vms`).
- **Gepunktete Pfeile**: Richtlinien/Ressourcen, die auf die Server wirken – Security Group
  (Self-Regel für Cluster-internen Verkehr, SSH/6443 nur von Admin-CIDR), Affinity Group
  (harte Anti-Affinität je Node) und Key Pair (SSH-Zugang).
- **Terraform-Module**: `modules/platform-lz` (PLZ: Network Area + DNS), `modules/alz-k8s`
  (SKE-Cluster), `modules/alz-vms` (3 Server-Nodes mit Netz, Security Group, Affinity Group,
  Key Pair).
