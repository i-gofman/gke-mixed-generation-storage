# A GKE cluster with one N2 pool and one N4 pool, pinned to versions new
# enough for the storage features that make the pair safe to run together.
#
# This module exists to encode the version floors and the boot disk rule. It
# is a reference, not a production baseline: no private nodes, no Workload
# Identity hardening, no network policy. Take the version pins and the node
# pool shapes; bring your own security posture.

locals {
  # N4 cannot boot from Persistent Disk. Getting this wrong fails at node pool
  # creation, which is the good case - the bad case is a ComputeClass that
  # sets it wrong and simply never produces a node.
  n4_boot_disk_type = "hyperdisk-balanced"
  n2_boot_disk_type = "pd-balanced"
}

resource "google_container_cluster" "this" {
  provider = google-beta

  name     = var.cluster_name
  project  = var.project_id
  location = var.region

  network    = var.network
  subnetwork = var.subnetwork

  # Node pools are managed separately below.
  remove_default_node_pool = true
  initial_node_count       = 1

  min_master_version  = var.min_master_version
  deletion_protection = var.deletion_protection

  release_channel {
    channel = var.release_channel
  }

  # type: dynamic StorageClasses are served by this driver. Without the addon
  # every PVC referencing pd.csi.storage.gke.io stays Pending.
  addons_config {
    gce_persistent_disk_csi_driver_config {
      enabled = true
    }
  }

  dynamic "cluster_autoscaling" {
    for_each = var.enable_node_autoprovisioning ? [1] : []
    content {
      enabled = true

      resource_limits {
        resource_type = "cpu"
        minimum       = 0
        maximum       = var.autoprovisioning_max_cpu
      }
      resource_limits {
        resource_type = "memory"
        minimum       = 0
        maximum       = var.autoprovisioning_max_memory
      }

      auto_provisioning_defaults {
        # Auto-provisioned pools inherit this boot disk type. Leaving it at the
        # default (pd-balanced) means NAP cannot create N4 pools at all.
        disk_type = local.n4_boot_disk_type
        oauth_scopes = [
          "https://www.googleapis.com/auth/cloud-platform",
        ]
      }
    }
  }

  resource_labels = var.labels

  lifecycle {
    ignore_changes = [
      # The control plane is upgraded by the release channel, not by Terraform.
      min_master_version,
    ]
  }
}

resource "google_container_node_pool" "n2" {
  provider = google-beta

  name     = "pool-n2"
  project  = var.project_id
  location = var.region
  cluster  = google_container_cluster.this.name

  version    = var.node_version
  node_count = var.n2_node_count

  node_config {
    machine_type = var.n2_machine_type
    disk_type    = local.n2_boot_disk_type
    disk_size_gb = 100

    # N2 has Local SSD; N4 does not. Anything mounting this cannot fall back.
    local_ssd_count = var.n2_local_ssd_count

    labels = merge(var.labels, {
      "storage-generation" = "persistent-disk"
    })

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform",
    ]

    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }
  }

  management {
    auto_repair  = true
    auto_upgrade = true
  }

  lifecycle {
    ignore_changes = [version]
  }
}

resource "google_container_node_pool" "n4" {
  provider = google-beta

  name     = "pool-n4"
  project  = var.project_id
  location = var.region
  cluster  = google_container_cluster.this.name

  version    = var.node_version
  node_count = var.n4_node_count

  node_config {
    machine_type = var.n4_machine_type

    # Not optional. N4 supports no Persistent Disk type, boot disk included,
    # and pool creation fails outright with the default pd-balanced.
    disk_type    = local.n4_boot_disk_type
    disk_size_gb = 100

    labels = merge(var.labels, {
      "storage-generation" = "hyperdisk"
    })

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform",
    ]

    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }
  }

  management {
    auto_repair  = true
    auto_upgrade = true
  }

  lifecycle {
    ignore_changes = [version]
  }
}
