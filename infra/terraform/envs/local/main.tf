data "docker_network" "wan" {
  name = "disavery-wan" # created by compose.yaml so the toolbox can join it
}

data "sops_file" "secrets" {
  source_file = var.secrets_file
}

locals {
  site_roles = { app = 1, db = 2, obj = 3 }
  site_cfg = {
    a = { enabled = var.site_a_enabled, base = 20, subnet = "172.31.1.0/24" }
    b = { enabled = var.site_b_enabled, base = 30, subnet = "172.31.2.0/24" }
  }
  enabled_sites = [for s, c in local.site_cfg : s if c.enabled]

  # Every address in the lab, including disabled sites, so DNS/inventory can refer to them.
  ip = merge(
    {
      dns     = cidrhost(var.wan_subnet, 10)
      edge    = cidrhost(var.wan_subnet, 11)
      webhook = cidrhost(var.wan_subnet, 12)
      vault   = cidrhost(var.wan_subnet, 40)
      restore = cidrhost(var.wan_subnet, 50)
    },
    {
      for pair in setproduct(keys(local.site_cfg), keys(local.site_roles)) :
      "${pair[1]}-${pair[0]}" => cidrhost(var.wan_subnet, local.site_cfg[pair[0]].base + local.site_roles[pair[1]])
    },
  )

  node_uploads = {
    "/root/.ssh/authorized_keys" = file(var.ssh_public_key_path)
  }
}
