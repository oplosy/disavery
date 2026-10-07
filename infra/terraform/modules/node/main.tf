# A lab machine: systemd + sshd container (see ADR 0001).

# Resolve the tag to an image ID: the provider stores the ID, so passing the tag
# would force a replacement on every apply.
data "docker_image" "this" {
  name = var.image
}

resource "docker_container" "this" {
  name       = var.name
  hostname   = var.name
  image      = data.docker_image.this.id
  privileged = true
  must_run   = true
  restart    = "no"
  # Docker reports "bridge" even when only networks_advanced are attached;
  # leaving it unset makes every plan replace the container.
  network_mode = "bridge"
  dns          = var.dns
  tmpfs = {
    "/run"      = "rw"
    "/run/lock" = "rw"
  }

  dynamic "networks_advanced" {
    for_each = var.networks
    content {
      name         = networks_advanced.value.name
      ipv4_address = networks_advanced.value.ipv4_address
      aliases      = [var.name]
    }
  }

  dynamic "upload" {
    for_each = var.uploads
    content {
      file    = upload.key
      content = upload.value
    }
  }

  dynamic "labels" {
    for_each = merge({ "disavery.env" = "drill", "disavery.node" = var.name }, var.labels)
    content {
      label = labels.key
      value = labels.value
    }
  }
}
