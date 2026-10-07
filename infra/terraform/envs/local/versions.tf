terraform {
  required_version = ">= 1.9.0"
  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "~> 3.0.2"
    }
    sops = {
      source  = "carlpett/sops"
      version = "~> 1.1.1"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
  }
}

provider "docker" {
  host = "unix:///var/run/docker.sock"
}

provider "sops" {}
