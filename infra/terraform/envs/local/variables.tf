variable "site_a_enabled" {
  type    = bool
  default = true
}

variable "site_b_enabled" {
  type    = bool
  default = false
}

variable "active_site" {
  description = "Site that receives traffic (DNS direct record, edge upstream, webhook allowlist)."
  type        = string
  default     = "a"
  validation {
    condition     = contains(["a", "b"], var.active_site)
    error_message = "active_site must be \"a\" or \"b\"."
  }
}

variable "wan_subnet" {
  type    = string
  default = "172.31.0.0/24"
}

variable "edge_host_port" {
  description = "Host port published for the edge proxy's HTTPS listener."
  type        = number
  default     = 8443
}

variable "node_image" {
  type    = string
  default = "disavery/node:local"
}

variable "ssh_public_key_path" {
  type    = string
  default = "/secrets/ssh/id_ed25519.pub"
}

variable "secrets_file" {
  type    = string
  default = "/secrets/local.sops.yaml"
}

variable "restore_enabled" {
  description = "Create the isolated restore node (set by the S6 runbook, removed by its cleanup)."
  type        = bool
  default     = false
}

variable "standby_site" {
  description = "Site that runs as a warm standby (\"\" for none). Set with `disavery env set`."
  type        = string
  default     = ""
  validation {
    condition     = contains(["", "a", "b"], var.standby_site) && var.standby_site != var.active_site
    error_message = "standby_site must be \"\", \"a\" or \"b\" and differ from active_site."
  }
}

variable "grafana_host_port" {
  description = "Host port published for Grafana (anonymous, read-only)."
  type        = number
  default     = 3000
}

variable "prometheus_host_port" {
  description = "Host port published for the Prometheus UI."
  type        = number
  default     = 9090
}
