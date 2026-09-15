# Sources

Every factual claim encoded in this repo — version floors, disk compatibility,
the free performance baseline — traces to one of the pages below.

**Verified: 2026-09-15.** Google changes these tables. If you are reading this
much later, re-verify before trusting the catalog, and please send a PR with
the new date.

## Primary

| Claim | Source |
|---|---|
| Automated disk type selection (`parameters.type: dynamic`), `disk-type-preference`, `pd-type`, `hyperdisk-type`, `use-allowed-disk-topology` | [GKE — About Hyperdisk](https://cloud.google.com/kubernetes-engine/docs/concepts/hyperdisk#automated_disk_type_selection) |
| Custom ComputeClasses: `priorities[]`, `whenUnsatisfiable`, `nodePoolAutoCreation`, `activeMigration`, per-priority `storage.bootDiskType` | [GKE — About custom compute classes](https://cloud.google.com/kubernetes-engine/docs/concepts/about-custom-compute-classes) |
| Which machine series support which Persistent Disk types | [Compute Engine — Persistent Disk, machine type support](https://cloud.google.com/compute/docs/disks/persistent-disks) |
| Which machine series support which Hyperdisk types, and which combinations are allowlisted | [Compute Engine — About Hyperdisk](https://cloud.google.com/compute/docs/disks/hyperdisks) |
| Hyperdisk Balanced included baseline: 3,000 IOPS, 140 MiB/s per volume | [Compute Engine — Hyperdisk Balanced](https://cloud.google.com/compute/docs/disks/hd-types/hyperdisk-balanced) |
| Local SSD availability by machine series (N4 has none) | [Compute Engine — Local SSD](https://cloud.google.com/compute/docs/disks/local-ssd) |
| Version floors for each feature | [GKE release notes](https://cloud.google.com/kubernetes-engine/docs/release-notes) |
| Generic ephemeral volumes | [Kubernetes — Ephemeral volumes](https://kubernetes.io/docs/concepts/storage/ephemeral-volumes/#generic-ephemeral-volumes) |
| `WaitForFirstConsumer` binding semantics | [Kubernetes — Volume binding mode](https://kubernetes.io/docs/concepts/storage/storage-classes/#volume-binding-mode) |
| ReadWriteOnce multi-attach behaviour | [Kubernetes — Access modes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#access-modes) |

## Version floors

Recorded in `internal/catalog/versions.yaml`, which is the single source of
truth for the CLI, the Terraform defaults and the docs.

| Feature | Minimum | Applies to |
|---|---|---|
| Custom ComputeClass | 1.30.3-gke.1451000 | control plane |
| ComputeClass `nodePoolAutoCreation` | 1.33.3-gke.1136000 | control plane |
| `use-allowed-disk-topology` | 1.34.1-gke.2541000 | **control plane and node pools** |
| Automated disk type selection | 1.35.3-gke.1290000 | control plane |

## Machine family capabilities

Recorded in `internal/catalog/machine-families.yaml`. The two rows that drive
almost every finding in this repo:

- **N4 / N4A / N4D** — no Persistent Disk of any type, boot disk included.
  Hyperdisk Balanced, Balanced High Availability, Throughput and ML. No Local
  SSD.
- **N2** — all four Persistent Disk types, Local SSD, and Hyperdisk Balanced
  and Balanced High Availability **by allowlist** (contact your account team).

## How to re-verify

The compatibility tables are rendered server-side and are easy to misread from
a summary. Fetch the page and read the table itself:

```bash
curl -sL https://cloud.google.com/compute/docs/disks/hyperdisks \
  | python3 -c "$(cat hack/extract-tables.py)"
```

Then update `internal/catalog/machine-families.yaml` and the `Verified:` date
at the top of this file in the same commit.

## Not sourced here

**Pricing.** Hyperdisk provisioned IOPS and throughput are priced per region
and change. This repo hardcodes no prices; `MGS102` reports the delta above the
free baseline and only converts it to currency if you pass
`--price-iops-month` and `--price-throughput-mibps-month`. Get current numbers
from the [Compute Engine disk pricing page](https://cloud.google.com/compute/disks-image-pricing).
