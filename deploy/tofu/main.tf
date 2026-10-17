resource "upcloud_server" "app" {
  hostname = var.hostname
  zone     = var.zone
  plan     = var.plan

  # Required by cloud-init based templates.
  metadata  = true
  user_data = file("${path.module}/cloud-init.yaml")

  template {
    storage = "Ubuntu Server 24.04 LTS (Noble Numbat)"
    size    = var.storage_size

    backup_rule {
      interval  = "daily"
      time      = "0200"
      retention = 7
    }
  }

  network_interface {
    type = "public"
  }

  login {
    user              = "deploy"
    keys              = [var.ssh_public_key]
    create_password   = false
    password_delivery = "none"
  }

  labels = {
    app = "kharoborathus"
  }

  # Postgres lives on this disk, and user_data and login force a replacement in the provider.
  lifecycle {
    prevent_destroy = true
    ignore_changes  = [user_data, login]
  }
}
