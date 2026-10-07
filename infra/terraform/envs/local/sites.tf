module "site" {
  source   = "../../modules/site"
  for_each = toset(local.enabled_sites)

  site        = each.key
  subnet      = local.site_cfg[each.key].subnet
  wan_network = data.docker_network.wan.name
  wan_ips     = { for role in keys(local.site_roles) : role => local.ip["${role}-${each.key}"] }
  image       = var.node_image
  dns         = [local.ip.dns]
  uploads     = local.node_uploads
}
