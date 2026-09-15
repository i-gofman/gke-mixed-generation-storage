output "cluster_name" {
  description = "Cluster name."
  value       = google_container_cluster.this.name
}

output "cluster_endpoint" {
  description = "Control plane endpoint."
  value       = google_container_cluster.this.endpoint
  sensitive   = true
}

output "control_plane_version" {
  description = "Control plane version actually running."
  value       = google_container_cluster.this.master_version
}

output "node_pools" {
  description = "Node pool name to machine type and boot disk type."
  value = {
    (google_container_node_pool.n2.name) = {
      machine_type   = var.n2_machine_type
      boot_disk_type = local.n2_boot_disk_type
    }
    (google_container_node_pool.n4.name) = {
      machine_type   = var.n4_machine_type
      boot_disk_type = local.n4_boot_disk_type
    }
  }
}

output "get_credentials_command" {
  description = "Command to configure kubectl against this cluster."
  value       = "gcloud container clusters get-credentials ${google_container_cluster.this.name} --region ${var.region} --project ${var.project_id}"
}
