output "network_area_id" {
  value = module.platform.network_area_id
}

output "projects" {
  value = {
    platform = module.platform.project_id
    k8s      = module.alz_k8s.project_id
    vms      = module.alz_vms.project_id
  }
}

output "dns_zone" {
  value = var.dns_zone_name
}

output "vm_nodes" {
  value = module.alz_vms.node_private_ips
}

output "vm_public_ip" {
  value = module.alz_vms.public_ip
}

output "k8s_cluster" {
  value = module.alz_k8s.cluster_name
}
