variable "project_id" {
  description = "Project that will hold the cluster."
  type        = string
}

variable "region" {
  description = "Region for the regional cluster and its node pools."
  type        = string
  default     = "us-central1"
}

variable "cluster_name" {
  description = "Cluster name."
  type        = string
  default     = "mixed-generation"
}

variable "network" {
  description = "VPC network self-link or name."
  type        = string
  default     = "default"
}

variable "subnetwork" {
  description = "Subnetwork self-link or name."
  type        = string
  default     = "default"
}

# The floors below are the whole point of this module. They come from
# internal/catalog/versions.yaml, which is the single source of truth the CLI
# checks against; keep the two in step.
variable "min_master_version" {
  description = <<-EOT
    Minimum control plane version.

    Defaults to the floor for automated disk type selection
    (parameters.type: dynamic). Lower this only if you are not using dynamic
    StorageClasses, and be aware of the other floors:

      1.30.3-gke.1451000  ComputeClass
      1.33.3-gke.1136000  ComputeClass nodePoolAutoCreation
      1.34.1-gke.2541000  use-allowed-disk-topology (cluster AND node pools)
      1.35.3-gke.1290000  automated disk type selection
  EOT
  type        = string
  default     = "1.35.3-gke.1290000"
}

variable "node_version" {
  description = <<-EOT
    Node pool version. Must be at least 1.34.1-gke.2541000 for
    use-allowed-disk-topology, which is enforced per node pool and not just on
    the control plane. A lagging pool does not error - it silently stops
    receiving Pods whose volumes use that StorageClass.
  EOT
  type        = string
  default     = "1.35.3-gke.1290000"
}

variable "release_channel" {
  description = "RAPID, REGULAR, STABLE, or UNSPECIFIED. The floors above land in RAPID first."
  type        = string
  default     = "RAPID"

  validation {
    condition     = contains(["RAPID", "REGULAR", "STABLE", "UNSPECIFIED"], var.release_channel)
    error_message = "release_channel must be one of RAPID, REGULAR, STABLE, UNSPECIFIED."
  }
}

variable "n2_machine_type" {
  description = "Machine type for the previous-generation pool."
  type        = string
  default     = "n2-standard-4"
}

variable "n4_machine_type" {
  description = "Machine type for the current-generation pool."
  type        = string
  default     = "n4-standard-4"
}

variable "n2_node_count" {
  description = "Nodes per zone in the N2 pool."
  type        = number
  default     = 1
}

variable "n4_node_count" {
  description = "Nodes per zone in the N4 pool."
  type        = number
  default     = 1
}

variable "n2_local_ssd_count" {
  description = <<-EOT
    Local SSD disks on the N2 pool. N4 has no Local SSD at all, so any
    workload that depends on this cannot fall back to N4.
  EOT
  type        = number
  default     = 0
}

variable "enable_node_autoprovisioning" {
  description = <<-EOT
    Required for ComputeClass nodePoolAutoCreation to do anything. Without it
    the ComputeClass is accepted by the API and the node pools never appear.
  EOT
  type        = bool
  default     = false
}

variable "autoprovisioning_max_cpu" {
  description = "Cluster-wide CPU ceiling for node auto-provisioning."
  type        = number
  default     = 64
}

variable "autoprovisioning_max_memory" {
  description = "Cluster-wide memory ceiling (GB) for node auto-provisioning."
  type        = number
  default     = 256
}

variable "labels" {
  description = "Labels applied to the cluster and node pools."
  type        = map(string)
  default     = {}
}

variable "deletion_protection" {
  description = "Terraform-side guard against destroying the cluster."
  type        = bool
  default     = true
}
