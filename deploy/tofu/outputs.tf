output "ipv4" {
  description = "Public IPv4. Point the domain's A record here."
  value       = upcloud_server.app.network_interface[0].ip_address
}
