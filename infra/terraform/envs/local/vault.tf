# The vault stands in for a separate cloud account holding immutable backups.
resource "docker_network" "vault" {
  name = "disavery-vault"
  ipam_config {
    subnet  = "172.31.3.0/24"
    gateway = "172.31.3.1" # Docker fills it in; unset would force replacement
  }
  labels {
    label = "disavery.env"
    value = "drill"
  }
}

module "vault" {
  source  = "../../modules/node"
  name    = "vault"
  image   = var.node_image
  dns     = [local.ip.dns]
  uploads = local.node_uploads
  networks = [
    { name = data.docker_network.wan.name, ipv4_address = local.ip.vault },
    { name = docker_network.vault.name },
  ]
  labels = { "disavery.role" = "vault" }
}
