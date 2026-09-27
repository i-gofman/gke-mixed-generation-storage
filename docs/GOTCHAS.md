# Gotchas

Things that are true, load-bearing, and not obvious from the product docs.

## The divide is asymmetric

The easy mental model — "old machines use Persistent Disk, new machines use
Hyperdisk" — is wrong in a way that will cost you a migration plan.

| | Persistent Disk | Hyperdisk |
|---|---|---|
| N2, N2D, C2, ... | yes, all types | Hyperdisk Balanced **by allowlist only** |
| N4, N4A, N4D | **none at all**, boot disk included | yes |

So N4 cannot take a step toward N2, but N2 can sometimes take a step toward N4 —
only if your project has been allowlisted by your account team. Do not design a
fallback that depends on N2 attaching Hyperdisk without confirming that first.

Source of truth in this repo: `internal/catalog/machine-families.yaml`.

## Disk type selection happens once

`parameters.type: dynamic` picks a disk type **at provisioning time**, for the
node the Pod was first scheduled to. It is not re-evaluated, ever.

This is the single most important thing to understand about the feature, and
it is the reason `dynamic` alone does not solve the problem. A volume
provisioned as `pd-balanced` because the first Pod landed on N2 is a
`pd-balanced` volume forever. Six weeks later a node upgrade moves the Pod to
N4 and you are back to `FailedAttachVolume`.

Two real mitigations:

- **`use-allowed-disk-topology: "true"`** — constrains scheduling to nodes that
  can attach what was provisioned. The cost is a permanently narrower
  scheduling domain.
- **Generic ephemeral volumes** — the disk is created with the Pod and dies with
  it, so nothing outlives the generation it was provisioned for. Only works for
  data you can afford to lose.

## Those two mitigations conflict with ComputeClass fallback

`use-allowed-disk-topology` narrows where a Pod can go. ComputeClass fallback
exists to widen it when capacity is short. Apply both to the same stateful
workload and the fallback cannot fire — you get `Pending` instead of a node of
the wrong generation.

That is usually the right outcome. It is not usually the outcome people expect
when they turn both on.

## `WaitForFirstConsumer` is mandatory, not a tuning knob

With `volumeBindingMode: Immediate` the disk is provisioned before a node is
chosen. "Pick the type that suits the node" then has no node to consult. Any
`type: dynamic` StorageClass with `Immediate` binding is misconfigured.

## The version floor for `use-allowed-disk-topology` applies to node pools too

**1.34.1-gke.2541000 on the cluster AND on every node pool.** Control-plane-only
is the common mistake, and it fails quietly: the lagging pool stops receiving
Pods whose volumes came from that StorageClass, and those Pods sit `Pending`
with nothing useful in the events.

```bash
kubectl get nodes -o custom-columns=\
NAME:.metadata.name,POOL:.metadata.labels.cloud\\.google\\.com/gke-nodepool,VERSION:.status.nodeInfo.kubeletVersion
```

## `FailedAttachVolume`, not `CrashLoopBackOff`

When this breaks, the container never starts. There are no application logs,
because there is no application process. If you are reading logs, you are in
the wrong place:

```bash
kubectl describe pod POD | grep -A5 Events
```

## The `disk-type-preference` default is `hyperdisk-type`

On any node that supports both, an unset `disk-type-preference` resolves to
Hyperdisk. Combined with the allowlist above, the default can resolve to
something your project cannot provision.

This is the parameter that actually configures the behaviour everyone assumes
they already have. "N4 gets Hyperdisk, N2 gets Persistent Disk" is not what
`type: dynamic` promises on its own — N2 is in the Hyperdisk Balanced support
matrix, so GKE may class it as "supports both" and take the Hyperdisk branch.
`disk-type-preference: pd-type` is what encodes the rule, and it is what the
manifests and the chart in this repo ship with. Choose `hyperdisk-type`
deliberately, or not at all.

## N4 has no Local SSD

Not "less", none. A ComputeClass that falls back from N2 to N4 silently drops
Local SSD scratch capacity. A workload that assumed it either degrades or fails
at mount time.

```bash
kubectl get nodes -L cloud.google.com/gke-local-ssd-count
```

## `activeMigration` is a reschedule

`activeMigration.optimizeRulePriority: true` moves running Pods back up to a
preferred machine family when capacity returns. For a Pod with a bound
Persistent Disk volume, that is an outage you scheduled yourself.

## `nodePoolAutoCreation` needs node auto-provisioning

`spec.nodePoolAutoCreation.enabled: true` is accepted by the API whether or not
the cluster has NAP enabled. Without NAP, the node pools are simply never
created and you are left looking at a valid-looking ComputeClass and a `Pending`
Pod.

```bash
gcloud container clusters describe CLUSTER \
  --format="value(autoscaling.enableNodeAutoprovisioning)"
```

Also: auto-provisioned pools inherit `auto_provisioning_defaults.disk_type`. If
that is left at `pd-balanced`, NAP cannot create N4 pools at all.

## Hyperdisk Balanced has a free performance baseline — and a bill above it

3,000 IOPS and 140 MiB/s per volume are included. Everything above that is
billable per volume for the life of the volume. Put
`provisioned-iops-on-create` on a StorageClass used by an autoscaling workload
and you have multiplied that by your replica count.

Check the machine type's own limits before provisioning performance: a small VM
cannot deliver what you are paying for.

## Existing volumes are not migrated

Applying a better StorageClass changes nothing about what is already bound. The
volumes that will break are the ones provisioned before you fixed the config.
`mixed-fleet-check` exists to find exactly those.
