# Terraform owns the address plan, so it also writes the Ansible inventory.
resource "local_file" "inventory" {
  filename        = "${path.module}/../../../ansible/inventory/hosts.yml"
  file_permission = "0644"
  content = yamlencode({
    all = {
      vars = { active_site = var.active_site, standby_site = var.standby_site }
      children = merge(
        {
          webhooks = { hosts = { webhook = { wan_ip = local.ip.webhook } } }
          vaults   = { hosts = { vault = { wan_ip = local.ip.vault } } }
          restores = { hosts = { for h in(var.restore_enabled ? ["restore"] : []) : h => { wan_ip = local.ip.restore } } }
          # Playbooks target the serving and the standby site through these groups.
          active  = { hosts = { for role in keys(local.site_roles) : "${role}-${var.active_site}" => {} } }
          standby = { hosts = { for role in keys(local.site_roles) : "${role}-${var.standby_site}" => {} if var.standby_site != "" } }
        },
        {
          for role in keys(local.site_roles) : role => {
            hosts = { for s in local.enabled_sites : "${role}-${s}" => {
              wan_ip      = local.ip["${role}-${s}"]
              site        = s
              zone_subnet = local.site_cfg[s].subnet
            } }
          }
        },
        {
          for s in keys(local.site_cfg) : "site_${s}" => {
            hosts = { for role in keys(local.site_roles) : "${role}-${s}" => {} if local.site_cfg[s].enabled }
          }
        },
      )
    }
  })
}
