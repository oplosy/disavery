variable "name" {
  description = "Container name and hostname."
  type        = string
}

variable "image" {
  description = "Node image name."
  type        = string
}

variable "networks" {
  description = "Networks to attach; the first one with an address is the WAN."
  type = list(object({
    name         = string
    ipv4_address = optional(string)
  }))
}

variable "dns" {
  description = "Upstream DNS servers for Docker's embedded resolver."
  type        = list(string)
  default     = []
}

variable "uploads" {
  description = "Files to place in the container at creation: path => content."
  type        = map(string)
  default     = {}
}

variable "labels" {
  description = "Extra container labels."
  type        = map(string)
  default     = {}
}
