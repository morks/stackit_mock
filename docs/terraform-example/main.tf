terraform {
  required_providers {
    stackit = {
      source = "stackitcloud/stackit"
    }
  }
}

variable "mock_url" {
  type    = string
  default = "http://localhost:9001"
}

variable "organization_id" {
  type    = string
  default = "11111111-1111-1111-1111-111111111111"
}

# Pro Service ein Pfad-Praefix (siehe docs/api-notes.md, Abschnitt 10).
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

resource "stackit_resourcemanager_project" "demo" {
  parent_container_id = var.organization_id
  name                = "mock-demo"
  owner_email         = "owner@example.com"
}

resource "stackit_service_account" "ci" {
  project_id = stackit_resourcemanager_project.demo.project_id
  name       = "ci"
}

resource "stackit_network" "net" {
  project_id         = stackit_resourcemanager_project.demo.project_id
  name               = "demo-net"
  ipv4_prefix_length = 24
}

resource "stackit_security_group" "sg" {
  project_id = stackit_resourcemanager_project.demo.project_id
  name       = "demo-sg"
}

resource "stackit_security_group_rule" "ssh" {
  project_id        = stackit_resourcemanager_project.demo.project_id
  security_group_id = stackit_security_group.sg.security_group_id
  direction         = "ingress"
  protocol = {
    name = "tcp"
  }
  port_range = {
    min = 22
    max = 22
  }
}

resource "stackit_public_ip" "ip" {
  project_id = stackit_resourcemanager_project.demo.project_id
}

resource "stackit_volume" "data" {
  project_id        = stackit_resourcemanager_project.demo.project_id
  name              = "demo-data"
  availability_zone = "eu01-1"
  size              = 20
}

resource "stackit_server" "vm" {
  project_id        = stackit_resourcemanager_project.demo.project_id
  name              = "demo-vm"
  machine_type      = "c2i.1"
  availability_zone = "eu01-1"
  boot_volume = {
    size        = 32
    source_type = "image"
    source_id   = "a0000000-0000-4000-8000-000000000001"
  }
  network_interfaces = [stackit_network_interface.nic.network_interface_id]
}

resource "stackit_network_interface" "nic" {
  project_id = stackit_resourcemanager_project.demo.project_id
  network_id = stackit_network.net.network_id
}

resource "stackit_ske_cluster" "k8s" {
  project_id             = stackit_resourcemanager_project.demo.project_id
  name                   = "demo"
  kubernetes_version_min = "1.33"
  node_pools = [{
    name               = "np"
    machine_type       = "g2i.2"
    os_version_min     = "4230.2.1"
    minimum            = 1
    maximum            = 2
    availability_zones = ["eu01-1"]
  }]
}

resource "stackit_postgresflex_instance" "pg" {
  project_id      = stackit_resourcemanager_project.demo.project_id
  name            = "demo-pg"
  acl             = ["10.0.0.0/8"]
  backup_schedule = "00 00 * * *"
  flavor = {
    cpu = 2
    ram = 8
  }
  replicas = 1
  storage = {
    class = "premium-perf6-stackit"
    size  = 20
  }
  version = "16"
}

resource "stackit_mongodbflex_instance" "mongo" {
  project_id      = stackit_resourcemanager_project.demo.project_id
  name            = "demo-mongo"
  acl             = ["10.0.0.0/8"]
  backup_schedule = "00 1 * * *"
  flavor = {
    cpu = 2
    ram = 8
  }
  replicas = 1
  storage = {
    class = "premium-perf2-mongodb"
    size  = 10
  }
  version = "7.0"
  options = {
    type                              = "Single"
    point_in_time_window_hours        = 2
    snapshot_retention_days           = 3
    daily_snapshot_retention_days     = 0
    weekly_snapshot_retention_weeks   = 3
    monthly_snapshot_retention_months = 1
  }
}
