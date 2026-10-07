# One site: a zone network plus app, db and obj nodes.
resource "docker_network" "zone" {
  name = "disavery-site-${var.site}"
  ipam_config {
    subnet  = var.subnet
    gateway = cidrhost(var.subnet, 1) # Docker fills it in; unset would force replacement
  }
  labels {
    label = "disavery.env"
    value = "drill"
  }
}

module "node" {
  source   = "../node"
  for_each = var.wan_ips

  name    = "${each.key}-${var.site}"
  image   = var.image
  dns     = var.dns
  uploads = var.uploads
  networks = [
    { name = var.wan_network, ipv4_address = each.value },
    { name = docker_network.zone.name },
  ]
  labels = {
    "disavery.site" = var.site
    "disavery.role" = each.key
  }
}
