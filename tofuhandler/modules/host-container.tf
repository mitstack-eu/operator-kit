# host.container: one container on a host, reached through the host's
# docker daemon. The docker provider waits for the container's health check
# to pass before the apply returns, which is the type's observe proof.

variable "docker_host" {
  type = string
}

variable "name" {
  type = string
}

variable "image" {
  type = string
}

variable "port" {
  type    = number
  default = 8080
}

variable "env" {
  type      = map(string)
  default   = {}
  sensitive = true
}

# The command the container's health check runs. Null runs none.
variable "health_command" {
  type    = string
  default = null
}

provider "docker" {
  host = var.docker_host
}

# The image stays cached on the host after a delete, so other containers of
# the same image keep it; the absence proof skips it.
resource "docker_image" "this" {
  name         = var.image
  keep_locally = true
}

resource "docker_container" "this" {
  name         = var.name
  image        = docker_image.this.image_id
  restart      = "unless-stopped"
  must_run     = true
  env          = [for k, v in var.env : "${k}=${v}"]
  wait         = var.health_command != null
  wait_timeout = 180

  dynamic "healthcheck" {
    for_each = var.health_command == null ? [] : [var.health_command]
    content {
      test     = ["CMD-SHELL", healthcheck.value]
      interval = "2s"
      timeout  = "5s"
      retries  = 60
    }
  }
}

output "url" {
  value = "http://${docker_container.this.network_data[0].ip_address}:${var.port}"
}
