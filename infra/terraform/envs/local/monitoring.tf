# Alerting for drill detection (spec §10), part of the global zone. The
# configuration never changes with the topology: Prometheus discovers its
# targets from files that Ansible writes into the shared volume
# disavery-monitoring, so a repoint does not restart it and alerts resolve
# normally.

resource "docker_image" "prometheus" {
  name         = "prom/prometheus:v3.15.0"
  keep_locally = true
}

resource "docker_image" "alertmanager" {
  name         = "prom/alertmanager:v0.34.1"
  keep_locally = true
}

resource "docker_container" "prometheus" {
  name    = "prometheus"
  image   = docker_image.prometheus.image_id
  restart = "unless-stopped"
  # See modules/node: Docker reports "bridge" for networks_advanced-only containers.
  network_mode = "bridge"
  command = [
    "--config.file=/etc/prometheus/prometheus.yml",
    "--storage.tsdb.path=/prometheus",
    "--storage.tsdb.retention.time=7d",
  ]

  ports {
    internal = 9090
    external = var.prometheus_host_port
  }

  networks_advanced {
    name         = data.docker_network.wan.name
    ipv4_address = local.ip.prometheus
  }

  upload {
    file    = "/etc/prometheus/prometheus.yml"
    content = file("${path.module}/monitoring/prometheus.yml")
  }
  upload {
    file    = "/etc/prometheus/rules/disavery.yml"
    content = file("${path.module}/monitoring/alerts.yml")
  }

  volumes {
    volume_name    = "disavery-monitoring" # created by compose.yaml, written by Ansible
    container_path = "/etc/prometheus/targets"
    read_only      = true
  }

  labels {
    label = "disavery.env"
    value = "drill"
  }
}

resource "docker_container" "alertmanager" {
  name    = "alertmanager"
  image   = docker_image.alertmanager.image_id
  restart = "unless-stopped"
  # See modules/node: Docker reports "bridge" for networks_advanced-only containers.
  network_mode = "bridge"
  command = [
    "--config.file=/etc/alertmanager/alertmanager.yml",
    "--storage.path=/alertmanager",
  ]

  networks_advanced {
    name         = data.docker_network.wan.name
    ipv4_address = local.ip.alertmanager
  }

  upload {
    file    = "/etc/alertmanager/alertmanager.yml"
    content = file("${path.module}/monitoring/alertmanager.yml")
  }

  labels {
    label = "disavery.env"
    value = "drill"
  }
}
