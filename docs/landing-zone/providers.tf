# Alle Service-Endpunkte zeigen auf den lokalen Mock (ein Pfad-Praefix je Service).
# Gegen die echte STACKIT-Cloud: die *_custom_endpoint-Zeilen entfernen und
# service_account_key_path setzen.
provider "stackit" {
  default_region        = var.region
  service_account_token = "mock-token"

  resourcemanager_custom_endpoint    = "${var.mock_url}/resourcemanager"
  service_account_custom_endpoint    = "${var.mock_url}/serviceaccount"
  iaas_custom_endpoint               = "${var.mock_url}/iaas"
  ske_custom_endpoint                = "${var.mock_url}/ske"
  service_enablement_custom_endpoint = "${var.mock_url}/serviceenablement"
  dns_custom_endpoint                = "${var.mock_url}/dns"
}
