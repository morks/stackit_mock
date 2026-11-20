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
variable "region" { type = string }

variable "kubernetes_version_min" {
  type    = string
  default = "1.33"
}

variable "node_pool" {
  description = "Node Pool des Clusters."
  type = object({
    machine_type = string
    minimum      = number
    maximum      = number
  })
  default = {
    machine_type = "g2i.2"
    minimum      = 3
    maximum      = 6
  }
}

# Das Label "networkArea" haengt das Projekt beim Anlegen an die Network Area.
# Es ist danach nicht mehr aenderbar.
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

# Gerouteter Netz-Ausschnitt aus dem Adressraum der Area; der Cluster laeuft darin.
resource "stackit_network" "this" {
  project_id  = stackit_resourcemanager_project.this.project_id
  name        = "${var.name}-net"
  ipv4_prefix = var.network_prefix
  routed      = true
}

resource "stackit_ske_cluster" "this" {
  project_id             = stackit_resourcemanager_project.this.project_id
  name                   = "k8s-${var.environment}"
  kubernetes_version_min = var.kubernetes_version_min

  network = {
    id = stackit_network.this.network_id
  }

  node_pools = [{
    name               = "default"
    machine_type       = var.node_pool.machine_type
    os_version_min     = "4230.2.1"
    minimum            = var.node_pool.minimum
    maximum            = var.node_pool.maximum
    availability_zones = ["${var.region}-1", "${var.region}-2", "${var.region}-3"]
  }]

  maintenance = {
    enable_kubernetes_version_updates    = true
    enable_machine_image_version_updates = true
    start                                = "01:00:00Z"
    end                                  = "02:00:00Z"
  }
}

output "project_id" {
  value = stackit_resourcemanager_project.this.project_id
}

output "cluster_name" {
  value = stackit_ske_cluster.this.name
}
