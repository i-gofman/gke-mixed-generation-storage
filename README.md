# gke-mixed-generation-storage

Tooling and reference configuration for running **N2 and N4 node pools in the
same GKE cluster** without stranding your volumes.

N4-family machines cannot attach Persistent Disk. Not a slower tier of it —
none of it, boot disk included. A `pd-balanced` volume that was provisioned
while its Pod ran on N2 keeps working right up until the Pod is rescheduled
onto N4, at which point it fails to attach and the Pod hangs in
`ContainerCreating`. There are no application logs, because the container never
starts.

This repo contains the audit tool that finds those volumes before a node
upgrade does, plus the manifests, Helm chart and Terraform that avoid the
problem in the first place.

Companion to the article *One StorageClass, Two VM Generations — how to run N2
and N4 nodes in the same GKE cluster without your Pods quietly hanging
forever*.

> Not an officially supported Google product. See [DISCLAIMER.md](DISCLAIMER.md).

---

## Quick start

```bash
go install github.com/igofman/gke-mixed-generation-storage/cmd/mixed-fleet-check@latest
mixed-fleet-check
```

Or grab a binary from [releases](https://github.com/igofman/gke-mixed-generation-storage/releases),
or run it from the container image:

```bash
docker run --rm -v ~/.kube:/.kube:ro -e KUBECONFIG=/.kube/config \
  ghcr.io/igofman/mixed-fleet-check:latest
```

Sample output:

```
SEVERITY  ID      RESOURCE                                  FINDING
ERROR     MGS001  PersistentVolume/pvc-8f21… (claim db/data)  Persistent Disk volume can be scheduled onto a Hyperdisk-only node
ERROR     MGS005  NodePools                                 Node pools are below the use-allowed-disk-topology floor
WARN      MGS100  StorageClass/premium-rwo                  type: dynamic StorageClass does not set disk-type-preference
WARN      MGS105  StorageClass/standard-rwo (default)       Default StorageClass provisions Persistent Disk on a fleet that includes Hyperdisk-only nodes
INFO      MGS200  Cluster                                   Fleet summary
```

Exit codes: `0` clean, `1` warnings, `2` errors, `3` the tool itself failed.
That makes it usable as a CI gate:

```bash
mixed-fleet-check --fail-on warn --format sarif > results.sarif
```

---

## What's in here

| Path | What it is |
|---|---|
| [`cmd/mixed-fleet-check`](cmd/mixed-fleet-check) | The audit CLI. Read-only; 18 checks. |
| [`internal/catalog`](internal/catalog) | Machine family capabilities and GKE version floors as data, not code. |
| [`manifests/`](manifests) | Plain YAML: the StorageClass, the ComputeClass, an ephemeral-volume Deployment. |
| [`charts/mixed-generation-storage`](charts/mixed-generation-storage) | Helm chart for the same, plus an optional audit CronJob. |
| [`terraform/`](terraform) | A cluster with N2 and N4 pools pinned to versions new enough for the features to work. |
| [`examples/`](examples) | Reproduce the failure, fix it, then force the fallback and measure what it cost. |
| [`docs/GOTCHAS.md`](docs/GOTCHAS.md) | The non-obvious behaviour, in one place. |
| [`docs/CHECKS.md`](docs/CHECKS.md) | Every check, why it exists, how to fix it. |
| [`docs/SOURCES.md`](docs/SOURCES.md) | Where every factual claim came from, and when it was verified. |

---

## The three things worth knowing

**The divide is asymmetric.** N4 supports no Persistent Disk. N2 supports all
Persistent Disk types *and* Hyperdisk Balanced — but the Hyperdisk half is
allowlisted, so you must talk to your account team before designing around it.
"Old uses PD, new uses Hyperdisk" is the wrong model. It is also why the
StorageClass here sets `disk-type-preference: pd-type`: because N2 counts as a
"supports both" node, GKE's own default would prefer Hyperdisk there, and that
is the combination you may not be allowed to provision.

**Disk type selection happens once.** `parameters.type: dynamic` picks a type at
provisioning time, for the node the Pod first landed on, and never revisits it.
It makes new volumes land correctly. It does nothing for the volume that is
already bound, which is the one that will break.

**The version floor for `use-allowed-disk-topology` applies per node pool.** Not
just the control plane. A pool you forgot to upgrade does not error — it stops
receiving those Pods, and they sit `Pending` with nothing useful in the events.

The rest is in [docs/GOTCHAS.md](docs/GOTCHAS.md).

---

## The configuration

A StorageClass that survives a mixed fleet:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: mixed-generation-safe
provisioner: pd.csi.storage.gke.io
parameters:
  type: dynamic                      # GKE picks per node.       1.35.3-gke.1290000+
  disk-type-preference: pd-type      # ...and PD on nodes that could take either.
  pd-type: pd-balanced
  hyperdisk-type: hyperdisk-balanced
  use-allowed-disk-topology: "true"  # ...and keeps it that way. 1.34.1-gke.2541000+
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
```

`WaitForFirstConsumer` is mandatory, not a preference: with `Immediate` binding
the disk is provisioned before a node is chosen, so there is no node for the
selection to consider.

Apply it:

```bash
kubectl apply -k manifests/
```

Or with Helm:

```bash
helm install mgs charts/mixed-generation-storage \
  --set computeClass.enabled=true \
  --set audit.enabled=true
```

The chart refuses to render a `type: dynamic` StorageClass with an empty
`disk-type-preference`, because falling back to the GKE default hides a
decision you should be making on purpose. It defaults to `pd-type`; set
`storageClass.diskTypePreference=hyperdisk-type` if your project is allowlisted
for Hyperdisk on previous-generation machines and you want it.

---

## Version floors

| Feature | Minimum | Applies to |
|---|---|---|
| Custom ComputeClass | `1.30.3-gke.1451000` | control plane |
| ComputeClass `nodePoolAutoCreation` | `1.33.3-gke.1136000` | control plane (+ node auto-provisioning) |
| `use-allowed-disk-topology` | `1.34.1-gke.2541000` | **control plane and every node pool** |
| Automated disk type selection (`type: dynamic`) | `1.35.3-gke.1290000` | control plane |

These live in [`internal/catalog/versions.yaml`](internal/catalog/versions.yaml),
which is the single source of truth for the CLI, the Terraform defaults and
these docs. Change them there.

---

## Machine family capabilities

[`internal/catalog/machine-families.yaml`](internal/catalog/machine-families.yaml)
is plain data. Adding a family needs no Go changes:

```yaml
n4:
  generation: 4
  persistent_disk: []        # none at all, boot disk included
  hyperdisk: [hyperdisk-balanced, hyperdisk-balanced-high-availability,
              hyperdisk-throughput, hyperdisk-ml]
  local_ssd: false
  boot_disk: [hyperdisk-balanced]
```

If `mixed-fleet-check` reports `MGS108` against a family you are running, that
family is missing from the catalog and was **not** checked. A PR adding it is
the most useful contribution to this repo.

Nothing here is specific to N2 and N4. The same one-way divide — older series on
Persistent Disk, newer series Hyperdisk-only — runs through N2D → N4D,
C2 → C3/C4 and M1 → M4, and through every generation that ships Hyperdisk-only
after them. The catalog already carries those families; a new generation needs a
catalog row and a ComputeClass priority, not a change to any application
manifest.

---

## Using it in CI

```yaml
- name: audit GKE storage configuration
  run: |
    mixed-fleet-check --format sarif --fail-on error > results.sarif

- uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: results.sarif
```

Or as a CronJob in the cluster, via the chart's `audit.enabled=true`.

---

## Flags

```
--kubeconfig PATH                   kubeconfig (default $KUBECONFIG, then ~/.kube/config)
--context NAME                      kubeconfig context
-n, --namespace NAME                limit workload inspection to one namespace
-o, --format table|json|sarif       output format (default table)
--fail-on error|warn|none           minimum severity that sets a non-zero exit code
--skip ID,ID                        suppress checks, e.g. --skip MGS102,MGS108
--price-iops-month FLOAT            your region's price per provisioned IOPS/month
--price-throughput-mibps-month FLOAT  your region's price per provisioned MiB/s/month
-v, --verbose                       expand informational findings
```

The two price flags exist because Hyperdisk pricing is regional and this repo
hardcodes no prices. Without them, `MGS102` tells you how far above the free
baseline (3,000 IOPS / 140 MiB/s per volume) you are provisioned; with them, it
converts that to a monthly figure.

---

## Development

```bash
make build     # bin/mixed-fleet-check
make test      # go test -race ./...
make lint      # vet, yamllint, kubeconform, helm lint, terraform, shellcheck
make audit     # build and run against the current kubectl context
```

Forking? `./hack/rename-module.sh github.com/your-org/your-repo` repoints the
Go module path.

## Contributing

Two things are especially welcome:

1. **Catalog corrections.** If a machine family's capabilities are wrong or
   out of date, fix `machine-families.yaml` and update the verification date in
   [docs/SOURCES.md](docs/SOURCES.md). Cite the Google doc.
2. **New checks.** Add a function to `internal/checks/`, register it in
   `All()`, document it in `docs/CHECKS.md`, and add a test case to
   `internal/checks/checks_test.go` — the tests build a `cluster.Snapshot`
   directly, so no cluster is needed.

Every claim in this repo should be traceable to a source. If you cannot cite
it, it does not go in the catalog.

## License

[Apache 2.0](LICENSE).
