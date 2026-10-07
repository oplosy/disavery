# Global zone: stands in for external DNS, CDN/edge and a third-party provider.
# Assumed to survive the loss of a site (ADR 0002).

resource "docker_image" "coredns" {
  name         = "coredns/coredns:1.11.3"
  keep_locally = true
}

resource "docker_image" "caddy" {
  name         = "caddy:2.8.4"
  keep_locally = true
}

resource "docker_container" "dns" {
  name    = "dns"
  image   = docker_image.coredns.image_id
  command = ["-conf", "/Corefile"]
  restart = "unless-stopped"
  # See modules/node: Docker reports "bridge" for networks_advanced-only containers.
  network_mode = "bridge"

  networks_advanced {
    name         = data.docker_network.wan.name
    ipv4_address = local.ip.dns
  }

  upload {
    file = "/Corefile"
    content = templatefile("${path.module}/templates/Corefile.tftpl", {
      edge_ip   = local.ip.edge
      direct_ip = local.ip["app-${var.active_site}"]
    })
  }

  labels {
    label = "disavery.env"
    value = "drill"
  }
}

resource "docker_container" "edge" {
  name    = "edge"
  image   = docker_image.caddy.image_id
  restart = "unless-stopped"
  # See modules/node: Docker reports "bridge" for networks_advanced-only containers.
  network_mode = "bridge"
  dns          = [local.ip.dns]

  ports {
    internal = 443
    external = var.edge_host_port
  }

  networks_advanced {
    name         = data.docker_network.wan.name
    ipv4_address = local.ip.edge
  }

  upload {
    file    = "/etc/caddy/Caddyfile"
    content = templatefile("${path.module}/templates/Caddyfile.tftpl", { upstream = "app-${var.active_site}:8080" })
  }
  upload {
    file    = "/etc/caddy/docs.crt"
    content = data.sops_file.secrets.data["tls.docs_crt"]
  }
  upload {
    file    = "/etc/caddy/docs.key"
    content = data.sops_file.secrets.data["tls.docs_key"]
  }

  labels {
    label = "disavery.env"
    value = "drill"
  }
}

module "webhook" {
  source  = "../../modules/node"
  name    = "webhook"
  image   = var.node_image
  dns     = [local.ip.dns]
  uploads = local.node_uploads
  networks = [
    { name = data.docker_network.wan.name, ipv4_address = local.ip.webhook },
  ]
  labels = { "disavery.role" = "webhook" }
}
