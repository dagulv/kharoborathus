variable "state_passphrase" {
  description = "Encrypts the local state and plans. Set TF_VAR_state_passphrase."
  type        = string
  sensitive   = true

  validation {
    condition     = length(var.state_passphrase) >= 16
    error_message = "The state passphrase needs at least 16 characters."
  }
}

variable "ssh_public_key" {
  description = "Public key for the deploy user."
  type        = string

  # The server ignores later key changes, so a placeholder would lock you out for good.
  validation {
    condition     = can(regex("^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)) [A-Za-z0-9+/]{60,}={0,3}( .*)?$", var.ssh_public_key))
    error_message = "Set ssh_public_key to your real public key, not the example."
  }
}

variable "haloy_api_domain" {
  description = "Domain for the Haloy API. haloy.yaml uses it as server."
  type        = string

  # Cloud-init runs once, so a wrong domain needs a manual `haloyd config set api-domain`.
  validation {
    condition     = can(regex("^[a-z0-9.-]+\\.[a-z]{2,}$", var.haloy_api_domain)) && !endswith(var.haloy_api_domain, "example.com")
    error_message = "Set haloy_api_domain to your real domain, not the example."
  }
}

variable "hostname" {
  description = "Server hostname."
  type        = string
  default     = "kharoborathus"
}

variable "zone" {
  description = "UpCloud zone. List them with `upctl zone list`."
  type        = string
  default     = "se-sto1"
}

variable "plan" {
  description = "UpCloud server plan. List them with `upctl server plans`."
  type        = string
  default     = "2xCPU-4GB"
}

variable "storage_size" {
  description = "System disk in GB. It also holds the Postgres volume."
  type        = number
  default     = 50
}
