# host.postgres-database: one Postgres server container on a host, its data
# directory on the host's block volume, and its password generated and kept
# in this resource's state. The settings match postgres-db's own: bounded
# WAL, and archiving on with a no-op archive command.

variable "docker_host" {
  type = string
}

variable "name" {
  type = string
}

variable "image" {
  type    = string
  default = "postgres:16-alpine"
}

variable "data_dir" {
  type = string
}

variable "db_name" {
  type = string
}

variable "username" {
  type = string
}

variable "max_wal_size_mb" {
  type    = number
  default = 512
}

variable "archive_timeout_sec" {
  type    = number
  default = 300
}

provider "docker" {
  host = var.docker_host
}

resource "random_password" "db" {
  length  = 32
  special = false
}

# The image stays cached on the host after a delete; the absence proof
# skips it.
resource "docker_image" "db" {
  name         = var.image
  keep_locally = true
}

resource "docker_container" "db" {
  name     = var.name
  image    = docker_image.db.image_id
  restart  = "unless-stopped"
  must_run = true
  env = [
    "POSTGRES_PASSWORD=${random_password.db.result}",
    "POSTGRES_DB=${var.db_name}",
    "POSTGRES_USER=${var.username}",
  ]
  command = [
    "postgres",
    "-c", "max_wal_size=${var.max_wal_size_mb}MB",
    "-c", "archive_timeout=${var.archive_timeout_sec}",
    "-c", "archive_mode=on",
    "-c", "archive_command=true",
  ]

  volumes {
    host_path      = var.data_dir
    container_path = "/var/lib/postgresql/data"
  }

  healthcheck {
    test     = ["CMD-SHELL", "pg_isready -U ${var.username} -d ${var.db_name}"]
    interval = "2s"
    timeout  = "5s"
    retries  = 60
  }
  wait         = true
  wait_timeout = 180
}

output "host" {
  value = docker_container.db.network_data[0].ip_address
}

output "port" {
  value = "5432"
}

output "dbName" {
  value = var.db_name
}

output "username" {
  value = var.username
}

output "password" {
  value     = random_password.db.result
  sensitive = true
}
