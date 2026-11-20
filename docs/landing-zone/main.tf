locals {
  name = "lz-${var.environment}"
}

# --- Ordnerstruktur ------------------------------------------------------------
# Organisation
#  |- platform       -> Platform Landing Zone (Netz, DNS)
#  `- applications   -> Application Landing Zones (K8s, VMs)
resource "stackit_resourcemanager_folder" "platform" {
  parent_container_id = var.organization_id
  name                = "platform"
  owner_email         = var.owner_email
  labels              = { environment = var.environment }
}

resource "stackit_resourcemanager_folder" "applications" {
  parent_container_id = var.organization_id
  name                = "applications"
  owner_email         = var.owner_email
  labels              = { environment = var.environment }
}

# --- Platform Landing Zone: Network Area + managed DNS --------------------------
module "platform" {
  source = "./modules/platform-lz"

  name                = "${local.name}-platform"
  organization_id     = var.organization_id
  parent_container_id = stackit_resourcemanager_folder.platform.container_id
  owner_email         = var.owner_email
  environment         = var.environment

  network_range    = var.area_network_range
  transfer_network = var.area_transfer_network
  nameservers      = var.area_nameservers
  dns_zone_name    = var.dns_zone_name
  contact_email    = var.owner_email
}

# --- Application Landing Zone 1: Kubernetes (SKE) -------------------------------
module "alz_k8s" {
  source = "./modules/alz-k8s"

  name                = "${local.name}-k8s"
  parent_container_id = stackit_resourcemanager_folder.applications.container_id
  owner_email         = var.owner_email
  environment         = var.environment
  network_area_id     = module.platform.network_area_id
  network_prefix      = var.k8s_network_prefix
  region              = var.region
}

# --- Application Landing Zone 2: 3 VMs fuer einen selbst betriebenen Cluster ----
module "alz_vms" {
  source = "./modules/alz-vms"

  name                = "${local.name}-vms"
  parent_container_id = stackit_resourcemanager_folder.applications.container_id
  owner_email         = var.owner_email
  environment         = var.environment
  network_area_id     = module.platform.network_area_id
  network_prefix      = var.vms_network_prefix
  admin_cidr          = var.admin_cidr
  ssh_public_key      = var.ssh_public_key
  region              = var.region
}

# --- DNS-Records ------------------------------------------------------------------
# Liegen im Root, weil sie die Zone der PLZ und die IPs der ALZ brauchen
# (in den Modulen wuerde das einen Zyklus ergeben).
resource "stackit_dns_record_set" "node" {
  for_each = module.alz_vms.node_private_ips

  project_id = module.platform.project_id
  zone_id    = module.platform.dns_zone_id
  name       = "${each.key}.vms.${var.dns_zone_name}."
  type       = "A"
  ttl        = 300
  records    = [each.value]
  comment    = "Cluster-Node ${each.key} (privat)"
}

resource "stackit_dns_record_set" "api" {
  project_id = module.platform.project_id
  zone_id    = module.platform.dns_zone_id
  name       = "api.vms.${var.dns_zone_name}."
  type       = "A"
  ttl        = 300
  records    = [module.alz_vms.public_ip]
  comment    = "Kubernetes-API / Ingress der selbst betriebenen Cluster-Nodes"
}
