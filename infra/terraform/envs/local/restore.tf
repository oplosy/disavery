# Isolated node for the random restore test (S6). It reaches only the vault
# (read via pgBackRest), never archives, and serves nothing; the drill creates
# it and its cleanup removes it.
module "restore" {
  source  = "../../modules/node"
  count   = var.restore_enabled ? 1 : 0
  name    = "restore"
  image   = var.node_image
  dns     = [local.ip.dns]
  uploads = local.node_uploads
  networks = [
    { name = data.docker_network.wan.name, ipv4_address = local.ip.restore },
  ]
  labels = { "disavery.role" = "restore" }
}
