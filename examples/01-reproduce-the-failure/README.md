# 01 — Reproduce the failure

Make the N2/N4 storage divide fail on purpose, so you recognise it in production.

**What you should see:** a Pod stuck in `ContainerCreating` with a
`FailedAttachVolume` event, *not* a `CrashLoopBackOff`. That distinction is the
whole diagnostic: the container never starts, so there is nothing in the
application logs. People lose hours here looking at the wrong signal.

## Prerequisites

A GKE cluster with two node pools:

```bash
gcloud container node-pools create pool-n2 \
  --cluster CLUSTER --num-nodes 1 --machine-type n2-standard-4

gcloud container node-pools create pool-n4 \
  --cluster CLUSTER --num-nodes 1 --machine-type n4-standard-4 \
  --disk-type hyperdisk-balanced
```

The `--disk-type hyperdisk-balanced` on the N4 pool is not optional: N4 cannot
boot from Persistent Disk, and the pool creation fails without it.

## Run it

```bash
kubectl apply -f .
```

`manifest.yaml` pins the Pod to the N2 pool, provisions a `pd-balanced` volume,
and waits for it to be `Running`. Then:

```bash
kubectl wait --for=condition=Ready pod -l app=pinned-pd --timeout=300s

# Move it to N4. The volume is already bound to a pd-balanced disk.
kubectl patch deployment pinned-pd --type=merge -p \
  '{"spec":{"template":{"spec":{"nodeSelector":{"cloud.google.com/machine-family":"n4"}}}}}'
```

## What to look at

```bash
kubectl get pods -l app=pinned-pd
# NAME                         READY   STATUS              RESTARTS   AGE
# pinned-pd-7d9c...            0/1     ContainerCreating   0          3m

kubectl describe pod -l app=pinned-pd | grep -A5 Events
#   Warning  FailedAttachVolume  ...  AttachVolume.Attach failed for volume "pvc-..."
```

The disk exists, the PVC is `Bound`, the node is `Ready`, and nothing will ever
attach them to each other. Disk type selection ran once, at provisioning time,
and the answer it gave is now wrong for where the Pod lives.

## Clean up

```bash
kubectl delete -f .
```

The PVC has `reclaimPolicy: Delete`, so the underlying disk goes with it.
