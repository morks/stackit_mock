terraform {
  required_providers {
    stackit = {
      source = "stackitcloud/stackit"
    }
  }
}

variable "name" { type = string }
variable "parent_container_id" { type = string }
variable "owner_email" { type = string }
variable "environment" { type = string }
variable "network_area_id" { type = string }
variable "network_prefix" { type = string }
variable "admin_cidr" { type = string }
variable "ssh_public_key" { type = string }
variable "region" { type = string }

variable "machine_type" {
  type    = string
  default = "c2i.4"
}

variable "image_id" {
  description = "Image-ID fuer die Boot-Volumes (im Mock: statisches Image)."
  type        = string
  default     = "a0000000-0000-4000-8000-000000000001"
}

variable "nodes" {
  description = "Cluster-Nodes und ihre Availability Zone (Verteilung ueber die Zonen)."
  type        = map(string)
  default = {
    "node-1" = "1"
    "node-2" = "2"
    "node-3" = "3"
  }
}

locals {
  # Ports eines selbst betriebenen Kubernetes (z. B. kubeadm/RKE2):
  # API-Server von aussen; alles andere nur zwischen den Nodes (Self-Regel unten).
  admin_ports = {
    ssh = 22
    api = 6443
  }
}

resource "stackit_resourcemanager_project" "this" {
  parent_container_id = var.parent_container_id
  name                = var.name
  owner_email         = var.owner_email
  labels = {
    environment = var.environment
    landingzone = "application"
    networkArea = var.network_area_id
  }
}

# Eigenes, gerouteter Netz nur fuer diese drei Nodes.
resource "stackit_network" "this" {
  project_id  = stackit_resourcemanager_project.this.project_id
  name        = "${var.name}-net"
  ipv4_prefix = var.network_prefix
  routed      = true
}

resource "stackit_security_group" "cluster" {
  project_id  = stackit_resourcemanager_project.this.project_id
  name        = "${var.name}-cluster"
  description = "Cluster-interner Verkehr und Admin-Zugriff"
}

# Alles zwischen den Mitgliedern der Gruppe (etcd 2379-2380, kubelet 10250,
# CNI/VXLAN, NodePorts ...): Regel mit der Gruppe selbst als Quelle.
resource "stackit_security_group_rule" "intra_cluster" {
  project_id               = stackit_resourcemanager_project.this.project_id
  security_group_id        = stackit_security_group.cluster.security_group_id
  direction                = "ingress"
  remote_security_group_id = stackit_security_group.cluster.security_group_id
  description              = "Verkehr zwischen den Cluster-Nodes"
}

resource "stackit_security_group_rule" "admin" {
  for_each = local.admin_ports

  project_id        = stackit_resourcemanager_project.this.project_id
  security_group_id = stackit_security_group.cluster.security_group_id
  direction         = "ingress"
  ip_range          = var.admin_cidr
  description       = "${each.key} von der Admin-CIDR"
  protocol = {
    name = "tcp"
  }
  port_range = {
    min = each.value
    max = each.value
  }
}

resource "stackit_key_pair" "this" {
  name       = "${var.name}-key"
  public_key = var.ssh_public_key
}

# Anti-Affinity: die Nodes landen auf verschiedenen Hypervisor-Hosts.
resource "stackit_affinity_group" "nodes" {
  project_id = stackit_resourcemanager_project.this.project_id
  name       = "${var.name}-nodes"
  policy     = "hard-anti-affinity"
}

resource "stackit_network_interface" "node" {
  for_each = var.nodes

  project_id         = stackit_resourcemanager_project.this.project_id
  network_id         = stackit_network.this.network_id
  name               = each.key
  security_group_ids = [stackit_security_group.cluster.security_group_id]
}

resource "stackit_server" "node" {
  for_each = var.nodes

  project_id         = stackit_resourcemanager_project.this.project_id
  name               = each.key
  machine_type       = var.machine_type
  availability_zone  = "${var.region}-${each.value}"
  keypair_name       = stackit_key_pair.this.name
  affinity_group     = stackit_affinity_group.nodes.affinity_group_id
  network_interfaces = [stackit_network_interface.node[each.key].network_interface_id]

  boot_volume = {
    size                  = 50
    source_type           = "image"
    source_id             = var.image_id
    performance_class     = "storage_premium_perf1"
    delete_on_termination = true
  }

  labels = {
    role = "cluster-node"
  }
}

# Daten-Volume je Node (z. B. fuer etcd/Container-Storage).
resource "stackit_volume" "data" {
  for_each = var.nodes

  project_id        = stackit_resourcemanager_project.this.project_id
  name              = "${each.key}-data"
  availability_zone = "${var.region}-${each.value}"
  size              = 100
  performance_class = "storage_premium_perf1"
}

resource "stackit_server_volume_attach" "data" {
  for_each = var.nodes

  project_id = stackit_resourcemanager_project.this.project_id
  server_id  = stackit_server.node[each.key].server_id
  volume_id  = stackit_volume.data[each.key].volume_id
}

# Oeffentliche IP nur an Node 1 (Bastion / API-Endpunkt).
resource "stackit_public_ip" "api" {
  project_id = stackit_resourcemanager_project.this.project_id
}

resource "stackit_public_ip_associate" "api" {
  project_id           = stackit_resourcemanager_project.this.project_id
  public_ip_id         = stackit_public_ip.api.public_ip_id
  network_interface_id = stackit_network_interface.node["node-1"].network_interface_id
}

output "project_id" {
  value = stackit_resourcemanager_project.this.project_id
}

output "node_private_ips" {
  value = { for k, n in stackit_network_interface.node : k => n.ipv4 }
}

output "public_ip" {
  value = stackit_public_ip.api.ip
}
