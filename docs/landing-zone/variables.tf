variable "mock_url" {
  description = "Basis-URL des Mocks (Port siehe stackit_mock -addr)."
  type        = string
  default     = "http://localhost:9001"
}

variable "region" {
  type    = string
  default = "eu01"
}

variable "organization_id" {
  description = "STACKIT-Organisation (UUID), unter der Folder und Network Area liegen."
  type        = string
  default     = "11111111-1111-4111-8111-111111111111"
}

variable "owner_email" {
  description = "Owner der Projekte (wird nur beim Anlegen verwendet)."
  type        = string
  default     = "platform-team@example.com"
}

variable "environment" {
  description = "Kurzname der Umgebung, Teil aller Namen."
  type        = string
  default     = "dev"
}

variable "area_network_range" {
  description = "Adressraum der Network Area, aus dem die Projekt-Netze geschnitten werden."
  type        = string
  default     = "10.100.0.0/16"
}

variable "area_transfer_network" {
  description = "Transfer-Netz der Network Area (darf sich nicht mit den Ranges ueberschneiden)."
  type        = string
  default     = "10.255.0.0/24"
}

variable "area_nameservers" {
  description = "Resolver, die Netzen der Area per DHCP mitgegeben werden."
  type        = list(string)
  default     = ["185.199.108.153", "185.199.109.153"]
}

variable "dns_zone_name" {
  description = "Delegierte DNS-Zone der Landing Zone (ohne abschliessenden Punkt)."
  type        = string
  default     = "dev.lz.example.com"
}

variable "k8s_network_prefix" {
  type    = string
  default = "10.100.1.0/24"
}

variable "vms_network_prefix" {
  type    = string
  default = "10.100.2.0/24"
}

variable "admin_cidr" {
  description = "Quelle, von der SSH und die Kubernetes-API auf die VMs erlaubt sind (z. B. Buero-/VPN-Egress)."
  type        = string
  default     = "203.0.113.0/24"
}

variable "ssh_public_key" {
  description = "Oeffentlicher SSH-Key fuer die VMs."
  type        = string
  default     = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMockMockMockMockMockMockMockMockMockMo landing-zone@example.com"
}
