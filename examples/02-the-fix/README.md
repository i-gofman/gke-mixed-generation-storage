# 02 — The fix

Two different fixes, because they solve two different problems. Pick by
whether the data has to survive a reschedule.

| | Generic ephemeral volume | `use-allowed-disk-topology` |
|---|---|---|
| Data survives reschedule | No | Yes |
| Pod can run on any generation | Yes | No — constrained to compatible nodes |
| Failure mode when capacity is short | none | Pod stays `Pending` |
| Good for | scratch, caches, spill, build agents | databases, queues, anything stateful |

They are not alternatives you should agonise over. Scratch storage should be
ephemeral regardless; the topology constraint is for the volumes you cannot
re-create.

## Apply

```bash
kubectl apply -f storageclass.yaml
kubectl apply -f ephemeral-deployment.yaml     # scratch path
kubectl apply -f statefulset-pinned.yaml       # durable path
```

## Why `WaitForFirstConsumer` is mandatory here

With `Immediate` binding the disk is provisioned before a node is chosen, so
"pick the disk type that fits the node" has no node to fit. The StorageClass in
this directory sets `WaitForFirstConsumer`; do not change it.

## Verify the dynamic selection actually happened

```bash
kubectl get pvc -l app=scratch-workload \
  -o custom-columns=NAME:.metadata.name,SC:.spec.storageClassName,PV:.spec.volumeName

# For each PV, what did GKE actually provision?
kubectl get pv -o custom-columns=\
NAME:.metadata.name,TYPE:.spec.csi.volumeAttributes.type,NODE:.spec.nodeAffinity.required.nodeSelectorTerms
```

Pods on N4 should show `hyperdisk-balanced`; Pods on N2 should show
`pd-balanced`. If every volume is the same type, check that your nodes really
are mixed — and that the control plane is at 1.35.3-gke.1290000 or later, or
`type: dynamic` is simply being ignored.

## The version floor that bites

`use-allowed-disk-topology` needs **1.34.1-gke.2541000 on the cluster and on
every node pool**. A node pool you forgot to upgrade does not error. It just
never receives these Pods, and they sit `Pending` with an unhelpful message.

```bash
kubectl get nodes -o custom-columns=\
NAME:.metadata.name,POOL:.metadata.labels.cloud\\.google\\.com/gke-nodepool,VERSION:.status.nodeInfo.kubeletVersion
```

Or let the tool do it: `mixed-fleet-check` reports this as `MGS005`.
