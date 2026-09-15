package checks

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/igofman/gke-mixed-generation-storage/internal/catalog"
	"github.com/igofman/gke-mixed-generation-storage/internal/cluster"
	"github.com/igofman/gke-mixed-generation-storage/internal/gkeversion"
)

const docHyperdisk = "https://cloud.google.com/kubernetes-engine/docs/concepts/hyperdisk"

// --- fleet helpers -------------------------------------------------------

type fleet struct {
	hyperdiskOnly []string // families present that cannot attach Persistent Disk
	pdOnly        []string // families present that cannot attach Hyperdisk
	both          []string // families present that can attach either
	unknown       []string
}

func classify(ctx Context) fleet {
	var f fleet
	for _, name := range ctx.Snap.Families() {
		fam, ok := ctx.Cat.Family(name)
		if !ok {
			f.unknown = append(f.unknown, name)
			continue
		}
		switch {
		case fam.SupportsBoth():
			f.both = append(f.both, name)
		case fam.SupportsHyperdisk():
			f.hyperdiskOnly = append(f.hyperdiskOnly, name)
		case fam.SupportsPersistentDisk():
			f.pdOnly = append(f.pdOnly, name)
		}
	}
	for _, s := range [][]string{f.hyperdiskOnly, f.pdOnly, f.both, f.unknown} {
		sort.Strings(s)
	}
	return f
}

// isMixed reports whether the fleet spans the Persistent Disk / Hyperdisk
// divide, which is the precondition for most of these findings mattering.
func (f fleet) isMixed() bool {
	return len(f.hyperdiskOnly) > 0 && (len(f.pdOnly) > 0 || len(f.both) > 0)
}

func isDynamic(sc storagev1.StorageClass) bool { return sc.Parameters["type"] == "dynamic" }

func usesPDCSI(sc storagev1.StorageClass) bool { return sc.Provisioner == cluster.PDCSIDriver }

func topologyEnabled(sc storagev1.StorageClass) bool {
	return strings.EqualFold(sc.Parameters["use-allowed-disk-topology"], "true")
}

func nodePoolsOf(ctx Context, families []string) []string {
	want := map[string]bool{}
	for _, f := range families {
		want[f] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range ctx.Snap.Nodes {
		if want[n.Family] && n.NodePool != "" && !seen[n.NodePool] {
			seen[n.NodePool] = true
			out = append(out, fmt.Sprintf("%s (%s)", n.NodePool, n.Family))
		}
	}
	sort.Strings(out)
	return out
}

// --- MGS001 --------------------------------------------------------------

func checkPDVolumeOnHyperdiskOnlyFleet(ctx Context) []Finding {
	f := classify(ctx)
	if len(f.hyperdiskOnly) == 0 {
		return nil
	}
	var out []Finding
	for _, v := range ctx.Snap.Volumes {
		if !v.IsCSI || !catalog.IsPersistentDiskType(v.DiskType) {
			continue
		}
		sc, ok := ctx.Snap.StorageClass(v.StorageClass)
		if ok && topologyEnabled(sc) {
			// use-allowed-disk-topology constrains scheduling for us.
			continue
		}
		out = append(out, Finding{
			ID:       "MGS001",
			Severity: SeverityError,
			Title:    "Persistent Disk volume can be scheduled onto a Hyperdisk-only node",
			Resource: fmt.Sprintf("PersistentVolume/%s (claim %s/%s)", v.PVName, v.ClaimNS, v.ClaimName),
			Detail: fmt.Sprintf(
				"This volume is %s (determined from %s) via StorageClass %q, which does not set "+
					"use-allowed-disk-topology. The cluster has %s node pools whose machine families "+
					"cannot attach Persistent Disk at all: %s. If the consuming Pod is ever rescheduled "+
					"onto one of them - node upgrade, Spot preemption, repair, or ComputeClass active "+
					"migration - the volume will not attach and the Pod will hang in ContainerCreating "+
					"with FailedAttachVolume.",
				v.DiskType, v.DiskTypeFrom, v.StorageClass,
				strings.Join(f.hyperdiskOnly, "/"), strings.Join(nodePoolsOf(ctx, f.hyperdiskOnly), ", ")),
			Remediation: "Set use-allowed-disk-topology: \"true\" on the StorageClass so GKE constrains " +
				"scheduling to compatible nodes, or move the workload to a generic ephemeral volume so " +
				"the disk is re-provisioned for whichever node the Pod lands on.",
			Doc: docHyperdisk,
		})
	}
	return out
}

// --- MGS004 --------------------------------------------------------------

func checkDynamicVersionFloor(ctx Context) []Finding {
	req := ctx.Cat.Require("dynamic_disk_type_selection")
	floor := gkeversion.MustParse(req.Min)
	server, err := gkeversion.Parse(ctx.Snap.ServerVersion)
	if err != nil {
		return nil
	}
	if server.AtLeast(floor) {
		return nil
	}
	var out []Finding
	for _, sc := range ctx.Snap.StorageClasses {
		if !isDynamic(sc) {
			continue
		}
		out = append(out, Finding{
			ID:       "MGS004",
			Severity: SeverityError,
			Title:    "StorageClass uses type: dynamic below the minimum GKE version",
			Resource: "StorageClass/" + sc.Name,
			Detail: fmt.Sprintf("Automated disk type selection requires %s; this control plane is %s. %s",
				req.Min, ctx.Snap.ServerVersion, req.Summary),
			Remediation: "Upgrade the control plane to " + req.Min + " or later, or use a fixed disk type until then.",
			Doc:         req.Doc,
		})
	}
	return out
}

// --- MGS005 --------------------------------------------------------------

func checkTopologyNodePoolFloor(ctx Context) []Finding {
	req := ctx.Cat.Require("use_allowed_disk_topology")
	floor := gkeversion.MustParse(req.Min)

	var users []string
	for _, sc := range ctx.Snap.StorageClasses {
		if topologyEnabled(sc) {
			users = append(users, sc.Name)
		}
	}
	if len(users) == 0 {
		return nil
	}

	behind := map[string]string{} // node pool -> version
	for _, n := range ctx.Snap.Nodes {
		v, err := gkeversion.Parse(n.KubeletVer)
		if err != nil || v.AtLeast(floor) {
			continue
		}
		pool := n.NodePool
		if pool == "" {
			pool = n.Name
		}
		behind[pool] = n.KubeletVer
	}

	var out []Finding
	if server, err := gkeversion.Parse(ctx.Snap.ServerVersion); err == nil && !server.AtLeast(floor) {
		out = append(out, Finding{
			ID:       "MGS005",
			Severity: SeverityError,
			Title:    "Control plane is below the use-allowed-disk-topology floor",
			Resource: "Cluster",
			Detail: fmt.Sprintf("StorageClass(es) %s set use-allowed-disk-topology, which requires %s; the control plane is %s.",
				strings.Join(users, ", "), req.Min, ctx.Snap.ServerVersion),
			Remediation: "Upgrade the control plane to " + req.Min + " or later.",
			Doc:         req.Doc,
		})
	}
	if len(behind) > 0 {
		var pools []string
		for p, v := range behind {
			pools = append(pools, fmt.Sprintf("%s (%s)", p, v))
		}
		sort.Strings(pools)
		out = append(out, Finding{
			ID:       "MGS005",
			Severity: SeverityError,
			Title:    "Node pools are below the use-allowed-disk-topology floor",
			Resource: "NodePools",
			Detail: fmt.Sprintf(
				"StorageClass(es) %s set use-allowed-disk-topology, which requires the cluster AND its "+
					"node pools at %s. Volumes provisioned by those StorageClasses will not schedule onto "+
					"these node pools: %s. Pods will stay Pending with no obvious cause.",
				strings.Join(users, ", "), req.Min, strings.Join(pools, ", ")),
			Remediation: "Upgrade these node pools to " + req.Min + " or later before relying on this StorageClass.",
			Doc:         req.Doc,
		})
	}
	return out
}

// --- MGS006 --------------------------------------------------------------

func checkCSIDriver(ctx Context) []Finding {
	if ctx.Snap.PDCSIInstalled {
		return nil
	}
	uses := false
	for _, sc := range ctx.Snap.StorageClasses {
		if usesPDCSI(sc) {
			uses = true
		}
	}
	if !uses {
		return nil
	}
	return []Finding{{
		ID:          "MGS006",
		Severity:    SeverityError,
		Title:       "Compute Engine persistent disk CSI driver is not installed",
		Resource:    "CSIDriver/" + cluster.PDCSIDriver,
		Detail:      "StorageClasses reference the pd.csi.storage.gke.io provisioner but the CSIDriver object is absent. No volume from those classes can be provisioned.",
		Remediation: "Enable the Compute Engine persistent disk CSI driver addon on the cluster.",
		Doc:         "https://cloud.google.com/kubernetes-engine/docs/how-to/persistent-volumes/gce-pd-csi-driver",
	}}
}

// --- MGS100 --------------------------------------------------------------

func checkMissingDiskTypePreference(ctx Context) []Finding {
	f := classify(ctx)
	var out []Finding
	for _, sc := range ctx.Snap.StorageClasses {
		if !isDynamic(sc) || sc.Parameters["disk-type-preference"] != "" {
			continue
		}
		detail := "With type: dynamic and no disk-type-preference, GKE defaults to hyperdisk-type on any " +
			"node that supports BOTH Persistent Disk and Hyperdisk."
		if len(f.both) > 0 {
			detail += fmt.Sprintf(" This cluster has such node pools: %s. Note that Hyperdisk Balanced on "+
				"previous-generation families is allowlisted (you must contact your account team), so relying "+
				"on the default here may not do what you expect.", strings.Join(f.both, ", "))
		} else {
			detail += " No dual-capable families are currently present, but adding one later would silently change behaviour."
		}
		out = append(out, Finding{
			ID:          "MGS100",
			Severity:    SeverityWarn,
			Title:       "type: dynamic StorageClass does not set disk-type-preference",
			Resource:    "StorageClass/" + sc.Name,
			Detail:      detail,
			Remediation: "Set disk-type-preference explicitly (pd-type or hyperdisk-type) rather than relying on the default.",
			Doc:         docHyperdisk,
		})
	}
	return out
}

// --- MGS101 --------------------------------------------------------------

func checkMissingAllowedDiskTopology(ctx Context) []Finding {
	var out []Finding
	for _, sc := range ctx.Snap.StorageClasses {
		if !isDynamic(sc) || topologyEnabled(sc) {
			continue
		}
		out = append(out, Finding{
			ID:       "MGS101",
			Severity: SeverityWarn,
			Title:    "type: dynamic StorageClass does not set use-allowed-disk-topology",
			Resource: "StorageClass/" + sc.Name,
			Detail: "Disk type selection runs once, at provisioning time. Without use-allowed-disk-topology " +
				"nothing stops the scheduler from later placing the Pod on a node that cannot attach the " +
				"type it already provisioned.",
			Remediation: "Set use-allowed-disk-topology: \"true\" (requires cluster and node pools at " +
				ctx.Cat.Require("use_allowed_disk_topology").Min + "), or use generic ephemeral volumes.",
			Doc: docHyperdisk,
		})
	}
	return out
}

// --- MGS102 --------------------------------------------------------------

func checkAboveBaseline(ctx Context) []Finding {
	base := ctx.Cat.Baseline
	var out []Finding
	for _, sc := range ctx.Snap.StorageClasses {
		if !usesPDCSI(sc) {
			continue
		}
		hdType := sc.Parameters["hyperdisk-type"]
		if !isDynamic(sc) {
			hdType = sc.Parameters["type"]
		}
		if !catalog.IsHyperdiskType(hdType) && !isDynamic(sc) {
			continue
		}

		var notes []string
		if iops, ok := parseInt(sc.Parameters["provisioned-iops-on-create"]); ok && iops > base.IOPS {
			delta := iops - base.IOPS
			note := fmt.Sprintf("%d IOPS provisioned, %d above the free baseline of %d", iops, delta, base.IOPS)
			if ctx.Opts.PriceIOPSPerMonth > 0 {
				note += fmt.Sprintf(" (~$%.2f/volume/month at the price you supplied)", float64(delta)*ctx.Opts.PriceIOPSPerMonth)
			}
			notes = append(notes, note)
		}
		if mib, ok := parseMiB(sc.Parameters["provisioned-throughput-on-create"]); ok && mib > base.ThroughputMiBPerS {
			delta := mib - base.ThroughputMiBPerS
			note := fmt.Sprintf("%d MiB/s provisioned, %d above the free baseline of %d", mib, delta, base.ThroughputMiBPerS)
			if ctx.Opts.PriceThroughputMiBPerMonth > 0 {
				note += fmt.Sprintf(" (~$%.2f/volume/month at the price you supplied)", float64(delta)*ctx.Opts.PriceThroughputMiBPerMonth)
			}
			notes = append(notes, note)
		}
		if len(notes) == 0 {
			continue
		}
		out = append(out, Finding{
			ID:       "MGS102",
			Severity: SeverityWarn,
			Title:    "Hyperdisk performance provisioned above the free baseline",
			Resource: "StorageClass/" + sc.Name,
			Detail: fmt.Sprintf("Hyperdisk Balanced includes %d IOPS and %d MiB/s per volume at no charge; "+
				"everything above that is billable, per volume, for the life of the volume. %s.",
				base.IOPS, base.ThroughputMiBPerS, strings.Join(notes, "; ")),
			Remediation: "Confirm the workload needs this. For ephemeral scratch volumes the baseline is " +
				"usually enough. Also check the VM's per-instance limits - a small machine type cannot " +
				"deliver what you are paying to provision.",
			Doc: base.Doc,
		})
	}
	return out
}

// --- MGS105 --------------------------------------------------------------

func checkDefaultStorageClass(ctx Context) []Finding {
	f := classify(ctx)
	if len(f.hyperdiskOnly) == 0 || ctx.Snap.DefaultSCName == "" {
		return nil
	}
	sc, ok := ctx.Snap.StorageClass(ctx.Snap.DefaultSCName)
	if !ok || !usesPDCSI(sc) || isDynamic(sc) {
		return nil
	}
	t := sc.Parameters["type"]
	if t == "" {
		t = "pd-balanced (driver default)"
	}
	if catalog.IsHyperdiskType(t) {
		return nil
	}
	return []Finding{{
		ID:       "MGS105",
		Severity: SeverityWarn,
		Title:    "Default StorageClass provisions Persistent Disk on a fleet that includes Hyperdisk-only nodes",
		Resource: "StorageClass/" + sc.Name + " (default)",
		Detail: fmt.Sprintf("The default StorageClass provisions %s. Any team that writes an ordinary PVC "+
			"without naming a StorageClass gets a Persistent Disk, which cannot attach to your %s nodes.",
			t, strings.Join(f.hyperdiskOnly, "/")),
		Remediation: "Make a type: dynamic StorageClass the cluster default so the safe path is the default path.",
		Doc:         docHyperdisk,
	}}
}

// --- MGS108 --------------------------------------------------------------

func checkUnknownFamilies(ctx Context) []Finding {
	f := classify(ctx)
	if len(f.unknown) == 0 {
		return nil
	}
	return []Finding{{
		ID:       "MGS108",
		Severity: SeverityWarn,
		Title:    "Machine families not present in the capability catalog",
		Resource: "Nodes",
		Detail: fmt.Sprintf("These families are running but not described in machine-families.yaml, so storage "+
			"compatibility was NOT checked for them: %s.", strings.Join(f.unknown, ", ")),
		Remediation: "Add them to internal/catalog/machine-families.yaml (no code changes needed) and open a PR.",
		Doc:         "https://cloud.google.com/compute/docs/disks/hyperdisks",
	}}
}

// --- MGS200 --------------------------------------------------------------

func checkFleetSummary(ctx Context) []Finding {
	f := classify(ctx)
	desc := func(label string, fams []string) string {
		if len(fams) == 0 {
			return ""
		}
		return fmt.Sprintf("%s: %s. ", label, strings.Join(fams, ", "))
	}
	detail := fmt.Sprintf("Control plane %s, %d nodes. ", ctx.Snap.ServerVersion, len(ctx.Snap.Nodes))
	detail += desc("Hyperdisk-only families", f.hyperdiskOnly)
	detail += desc("Persistent Disk-only families", f.pdOnly)
	detail += desc("Dual-capable families", f.both)
	if f.isMixed() {
		detail += "This is a mixed-generation fleet: the Persistent Disk / Hyperdisk divide applies."
	} else {
		detail += "No Persistent Disk / Hyperdisk divide detected in the current node set."
	}
	return []Finding{{
		ID: "MGS200", Severity: SeverityInfo, Title: "Fleet summary", Resource: "Cluster", Detail: detail,
	}}
}

// --- small parsers -------------------------------------------------------

func parseInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0, false
	}
	return int(q.Value()), true
}

// parseMiB reads a throughput parameter such as "250Mi" and returns MiB/s.
func parseMiB(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0, false
	}
	return int(q.Value() / (1024 * 1024)), true
}

func hasAccessMode(modes []corev1.PersistentVolumeAccessMode, want corev1.PersistentVolumeAccessMode) bool {
	for _, m := range modes {
		if m == want {
			return true
		}
	}
	return false
}
