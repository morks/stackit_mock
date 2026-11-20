terraform {
  required_providers {
    stackit = {
      source = "stackitcloud/stackit"
    }
  }
}

variable "name" { type = string }
variable "organization_id" { type = string }
variable "parent_container_id" { type = string }
variable "owner_email" { type = string }
variable "environment" { type = string }
variable "network_range" { type = string }
variable "transfer_network" { type = string }
variable "nameservers" { type = list(string) }
variable "dns_zone_name" { type = string }
variable "contact_email" { type = string }

# Projekt der Plattform: haelt die DNS-Zone und ist der Ort fuer weitere
# gemeinsame Dienste (Logging, Observability, ...).
resource "stackit_resourcemanager_project" "platform" {
  parent_container_id = var.parent_container_id
  name                = var.name
  owner_email         = var.owner_email
  labels = {
    environment = var.environment
    landingzone = "platform"
  }
}

# Network Area: gemeinsamer, gerouteter Adressraum fuer alle Application Landing Zones.
resource "stackit_network_area" "this" {
  organization_id = var.organization_id
  name            = "${var.name}-area"
  labels = {
    environment = var.environment
  }
}

resource "stackit_network_area_region" "this" {
  organization_id = var.organization_id
  network_area_id = stackit_network_area.this.network_area_id

  ipv4 = {
    transfer_network      = var.transfer_network
    network_ranges        = [{ prefix = var.network_range }]
    default_nameservers   = var.nameservers
    default_prefix_length = 24
    min_prefix_length     = 24
    max_prefix_length     = 28
  }
}

# Managed DNS: eine Zone pro Umgebung, Records pflegen die Landing Zones selbst.
resource "stackit_dns_zone" "this" {
  project_id    = stackit_resourcemanager_project.platform.project_id
  name          = "${var.name}-zone"
  dns_name      = var.dns_zone_name
  contact_email = var.contact_email
  description   = "Delegierte Zone der Landing Zone (${var.environment})"
  type          = "primary"
  default_ttl   = 3600
}

output "project_id" {
  value = stackit_resourcemanager_project.platform.project_id
}

output "network_area_id" {
  # erst verfuegbar, wenn auch die Region (Ranges, Transfernetz) konfiguriert ist
  value      = stackit_network_area.this.network_area_id
  depends_on = [stackit_network_area_region.this]
}

output "dns_zone_id" {
  value = stackit_dns_zone.this.zone_id
}
