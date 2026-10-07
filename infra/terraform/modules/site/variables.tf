variable "site" {
  description = "Site key, e.g. \"a\"."
  type        = string
}

variable "subnet" {
  description = "Zone network CIDR."
  type        = string
}

variable "wan_network" {
  type = string
}

variable "wan_ips" {
  description = "Role => WAN address, e.g. { app = \"172.31.0.21\" }."
  type        = map(string)
}

variable "image" {
  type = string
}

variable "dns" {
  type = list(string)
}

variable "uploads" {
  type = map(string)
}
