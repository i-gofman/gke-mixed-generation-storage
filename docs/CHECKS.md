# Checks

Every check `mixed-fleet-check` runs, why it exists, and how to make it stop.

IDs are stable. `MGS0xx` are errors, `MGS1xx` are warnings, `MGS2xx` is
informational. Suppress any of them with `--skip MGS102,MGS108`.

Two things the tool deliberately does **not** do:

- **It never writes to the cluster.** Read-only, always. The RBAC in the Helm
  chart grants `get` and `list` and nothing else.
- **It never invents prices.** Hyperdisk pricing is regional. `MGS102` reports
  how far above the free baseline you are provisioned; supply
  `--price-iops-month` and `--price-throughput-mibps-month` if you want that
  turned into a number.

---

## Errors

### MGS001 — Persistent Disk volume can be scheduled onto a Hyperdisk-only node

A bound PV whose disk type is `pd-*`, in a cluster that has node pools of a
machine family that cannot attach Persistent Disk at all (N4, N4A, N4D, C4,
and others — see `internal/catalog/machine-families.yaml`).

This is the headline failure. The volume works today because the Pod happens to
be on a compatible node. Any reschedule — node upgrade, Spot preemption, node
repair, ComputeClass active migration, a simple `kubectl delete pod` — can land
it on a node that cannot attach the disk. The Pod then sits in
`ContainerCreating` with `FailedAttachVolume`, not `CrashLoopBackOff`, so there
is nothing in the application logs to find.

**Fix:** set `use-allowed-disk-topology: "true"` on the StorageClass so GKE
constrains scheduling, or move the workload to a generic ephemeral volume so the
disk is provisioned fresh for whichever node the Pod lands on.

**Suppressed automatically** when the StorageClass already sets
`use-allowed-disk-topology`.

### MGS002 — Multi-replica Deployment shares one ReadWriteOnce PVC

Unrelated to machine generations, but it shows up constantly in the same
clusters and produces the same symptom. A `ReadWriteOnce` volume attaches to one
node; replicas beyond the first stay in `ContainerCreating` with
`Multi-Attach error`.

**Fix:** one PVC per replica (StatefulSet, or generic ephemeral volumes), or a
ReadWriteMany filesystem.

### MGS003 — ComputeClass priority requests a boot disk its machine family cannot use

A `priorities[]` entry that pairs, for example, `machineFamily: n4` with
`storage.bootDiskType: pd-balanced`. N4 cannot boot from Persistent Disk.

The CRD accepts this. Nodes are simply never created from that priority, and
the scale-up failure is reported somewhere you are not looking.

**Fix:** set `bootDiskType` per priority — `hyperdisk-balanced` for N4,
`pd-balanced` for N2.

### MGS004 — `type: dynamic` StorageClass below the minimum GKE version

Automated disk type selection requires control plane **1.35.3-gke.1290000**.
Below it the parameter is not honoured and you get the driver default instead
of the selection you asked for.

### MGS005 — `use-allowed-disk-topology` below its version floor

Requires **1.34.1-gke.2541000 on the control plane and on every node pool**.
This check reports the two separately, because the node pool half is the one
people miss: a pool you forgot to upgrade does not error, it just stops
receiving these Pods, and they sit `Pending`.

### MGS006 — Compute Engine persistent disk CSI driver is not installed

StorageClasses name `pd.csi.storage.gke.io` but the `CSIDriver` object is
absent. Nothing from those classes can be provisioned at all.

### MGS007 — ComputeClass below the version floor

ComputeClass itself requires **1.30.3-gke.1451000**.

### MGS008 — `nodePoolAutoCreation` may not be doing anything

`spec.nodePoolAutoCreation.enabled: true` also requires cluster-level node
auto-provisioning. Below **1.33.3-gke.1136000** this is a hard dependency; the
API accepts the field regardless and no node pools appear.

The tool cannot read the cluster's NAP setting from the Kubernetes API, so this
is a warning telling you to check:

```bash
gcloud container clusters describe CLUSTER \
  --format="value(autoscaling.enableNodeAutoprovisioning)"
```

---

## Warnings

### MGS100 — `type: dynamic` StorageClass does not set `disk-type-preference`

On a node that supports **both** disk families, GKE defaults to
`hyperdisk-type`. That default may be wrong for you, and it is invisible in
your manifests. It matters most because Hyperdisk Balanced on
previous-generation families (N2, C2, ...) is **allowlisted** — you must
contact your account team — so the default can resolve to something your
project cannot actually use.

**Fix:** set it explicitly. On an N2/N4 fleet the value you almost certainly
want is `pd-type` — Hyperdisk on N4, Persistent Disk on N2. `hyperdisk-type` is
a valid answer only if you have been allowlisted for Hyperdisk on the
previous-generation families you run.

### MGS101 — `type: dynamic` StorageClass does not set `use-allowed-disk-topology`

Disk type selection runs **once**, at provisioning time. `dynamic` gets the
first placement right and does nothing about the second one. Without the
topology constraint, nothing stops a later reschedule from stranding the
volume — which is MGS001, arriving a month later.

### MGS102 — Hyperdisk performance provisioned above the free baseline

Hyperdisk Balanced includes **3,000 IOPS and 140 MiB/s per volume** at no
charge. Everything above that is billable, per volume, for the volume's whole
life. A `provisioned-iops-on-create` on a StorageClass used by an autoscaling
workload multiplies by every replica.

Also check the VM's own limits: a small machine type cannot deliver what you
are paying to provision.

### MGS103 — StatefulSet volumeClaimTemplates are generation-pinned

Each replica's volume takes its type from the node the replica first landed on.
Scale from 3 to 5 after adding N4 capacity and replicas 3–4 get a different disk
type, and a different performance envelope, from replicas 0–2. Nothing reports
this; you find it in a latency graph.

### MGS104 — Local SSD capability is lost on fallback

A ComputeClass with an N2-style priority that has Local SSD and an N4-style
priority that cannot have any. N4 has no Local SSD. A workload that depends on
it either degrades silently or fails at mount time, depending on how it was
written.

### MGS105 — Default StorageClass provisions Persistent Disk on a mixed fleet

Any team writing an ordinary PVC without naming a StorageClass gets a
Persistent Disk that cannot attach to your Hyperdisk-only nodes. The safe path
should be the default path.

### MGS106 — ComputeClass uses `whenUnsatisfiable: ScaleUpAnyway`

Pods can be placed on a machine type outside your priority list — including one
that cannot attach their storage. `DoNotScaleUp` keeps the failure loud and
legible (`Pending`) instead of quiet and wrong.

### MGS107 — `activeMigration` with bound Persistent Disk volumes

`activeMigration.optimizeRulePriority: true` moves running Pods back to a
higher-priority machine family when capacity returns. That migration **is** a
reschedule. For a Pod holding a bound `pd-balanced` volume it is a scheduled
outage.

Fine for stateless Pods on ephemeral volumes. Turn it off for anything with
durable state.

### MGS108 — Machine families not in the capability catalog

Storage compatibility was **not** checked for these families. Add them to
`internal/catalog/machine-families.yaml` — no code change needed — and send a
PR.

---

## Informational

### MGS200 — Fleet summary

Control plane version, node count, and which families present fall on which
side of the Persistent Disk / Hyperdisk divide. Always emitted; it is the
context for everything above.
