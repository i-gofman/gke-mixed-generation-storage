# 03 — Force the ComputeClass fallback

A ComputeClass that prefers N4 and falls back to N2 looks like a pure win until
you notice what changes when the fallback fires. This example makes the
fallback happen on demand so you can measure it before production does.

## Prerequisites

Node auto-provisioning must be enabled on the cluster, or
`nodePoolAutoCreation` in the ComputeClass is silently inert:

```bash
gcloud container clusters describe CLUSTER \
  --format="value(autoscaling.enableNodeAutoprovisioning)"
```

If that prints `False`, either enable NAP or pre-create both node pools and set
`nodePoolAutoCreation.enabled: false`. `mixed-fleet-check` reports the
mismatch as `MGS008` — with the caveat that it cannot read the cluster's NAP
setting from the Kubernetes API, so it can only tell you to check.

## Apply

```bash
kubectl apply -f computeclass.yaml
kubectl apply -f workload.yaml
```

## Force the fallback

The honest way to do this is to make N4 genuinely unavailable:

```bash
# Cordon the N4 pool so the scheduler cannot use it.
kubectl cordon -l cloud.google.com/machine-family=n4

# Or take the pool to zero and let the ComputeClass fall through.
gcloud container clusters resize CLUSTER --node-pool pool-n4 --num-nodes 0
```

Then scale the workload up and watch where the new Pods land:

```bash
kubectl scale deployment fallback-demo --replicas=6
kubectl get pods -o wide -l app=fallback-demo
```

## What changed, and what it costs you

Three things are different on the N2 side, and none of them produce an error:

**No Local SSD.** N4 has none at all; N2 does. If you had it the other way
round — a workload depending on Local SSD scratch — the fallback to a family
without it gives you a Pod that runs and is slower, or one that fails at
startup when the mount is missing. Check with:

```bash
kubectl get nodes -L cloud.google.com/gke-local-ssd-count
```

**Different disk type, therefore different performance envelope.** A volume
provisioned on N2 as `pd-balanced` has different IOPS and throughput scaling
than a `hyperdisk-balanced` volume on N4. Your p99 moves. Nothing alerts.

**Hyperdisk Balanced on N2 is allowlisted.** N2 *can* attach Hyperdisk Balanced,
but only if your project has been allowlisted by your account team. Do not plan
a fallback around it without confirming that first — the divide is asymmetric,
not symmetric.

## The migration trap

`activeMigration.optimizeRulePriority: true` moves running Pods back up to N4
when capacity returns. For a stateless Pod on an ephemeral volume that is
exactly what you want. For a Pod holding a bound `pd-balanced` volume it is a
scheduled outage: the migration is a reschedule, the reschedule lands on N4,
and N4 cannot attach Persistent Disk. `computeclass.yaml` leaves it off and
says why. `mixed-fleet-check` flags the combination as `MGS107`.

## Clean up

```bash
kubectl uncordon -l cloud.google.com/machine-family=n4
kubectl delete -f workload.yaml -f computeclass.yaml
```
