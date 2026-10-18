package checks

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/igofman/gke-mixed-generation-storage/internal/catalog"
	"github.com/igofman/gke-mixed-generation-storage/internal/gkeversion"
)

const docComputeClass = "https://cloud.google.com/kubernetes-engine/docs/concepts/about-custom-compute-classes"

// --- MGS002 --------------------------------------------------------------

func checkSharedRWOMultiReplica(ctx Context) []Finding {
	// Index bound volumes by namespace/claim so we can read their access modes.
	type key struct{ ns, name string }
	byClaim := map[key][]corev1.PersistentVolumeAccessMode{}
	for _, v := range ctx.Snap.Volumes {
		if v.ClaimName != "" {
			byClaim[key{v.ClaimNS, v.ClaimName}] = v.AccessModes
		}
	}

	var out []Finding
	for _, w := range ctx.Snap.Workloads {
		if w.Kind != "Deployment" || w.Replicas <= 1 {
			continue
		}
		for _, vol := range w.Volumes {
			if vol.Ephemeral || vol.ClaimName == "" {
				continue
			}
			modes, ok := byClaim[key{w.Namespace, vol.ClaimName}]
			if !ok {
				continue // claim not bound yet; nothing to assert
			}
			if hasAccessMode(modes, corev1.ReadWriteMany) || hasAccessMode(modes, corev1.ReadWriteOncePod) {
				continue
			}
			if !hasAccessMode(modes, corev1.ReadWriteOnce) {
				continue
			}
			out = append(out, Finding{
				ID:       "MGS002",
				Severity: SeverityError,
				Title:    "Multi-replica Deployment shares one ReadWriteOnce volume",
				Resource: fmt.Sprintf("Deployment/%s/%s", w.Namespace, w.Name),
				Detail: fmt.Sprintf("%d replicas all mount PVC %q, which is ReadWriteOnce. A RWO volume attaches "+
					"to one node at a time, so as soon as two replicas land on different nodes the second fails "+
					"with a Multi-Attach error and hangs.", w.Replicas, vol.ClaimName),
				Remediation: "Give each Pod its own disk with a generic ephemeral volume " +
					"(spec.volumes[].ephemeral.volumeClaimTemplate), or scale to one replica, or convert to a " +
					"StatefulSet if the data must actually persist per replica.",
				Doc: "https://kubernetes.io/docs/concepts/storage/ephemeral-volumes/#generic-ephemeral-volumes",
			})
		}
	}
	return out
}

// --- MGS003 --------------------------------------------------------------

func checkComputeClassBootDisk(ctx Context) []Finding {
	var out []Finding
	for _, cc := range ctx.Snap.ComputeClasses {
		for i, p := range cc.Spec.Priorities {
			boot := p.Storage.BootDiskType
			if boot == "" {
				continue
			}
			famName := p.MachineFamily
			if famName == "" {
				famName = catalog.FamilyOfMachineType(p.MachineType)
			}
			if famName == "" {
				continue
			}
			fam, ok := ctx.Cat.Family(famName)
			if !ok {
				continue
			}
			if fam.Supports(boot) {
				continue
			}
			out = append(out, Finding{
				ID:       "MGS003",
				Severity: SeverityError,
				Title:    "ComputeClass priority rule requests an unsupported boot disk type",
				Resource: fmt.Sprintf("ComputeClass/%s priorities[%d]", cc.Name, i),
				Detail: fmt.Sprintf("Priority rule targets machine family %q with bootDiskType %q, which that "+
					"family cannot attach. Node pool creation for this rule will fail. %s",
					famName, boot, bootHint(fam)),
				Remediation: "Set storage.bootDiskType per priority rule, not once for the whole class: " +
					"Hyperdisk-only families need hyperdisk-balanced, earlier generations want pd-balanced.",
				Doc: docComputeClass,
			})
		}
	}
	return out
}

func bootHint(f catalog.Family) string {
	if len(f.BootDisk) > 0 {
		return "Supported boot disk types: " + strings.Join(f.BootDisk, ", ") + "."
	}
	if f.SupportsPersistentDisk() {
		return "Supported types include: " + strings.Join(f.PersistentDisk, ", ") + "."
	}
	return ""
}

// --- MGS007 --------------------------------------------------------------

func checkComputeClassVersionFloor(ctx Context) []Finding {
	if len(ctx.Snap.ComputeClasses) == 0 {
		return nil
	}
	req := ctx.Cat.Require("computeclass")
	floor := gkeversion.MustParse(req.Min)
	server, err := gkeversion.Parse(ctx.Snap.ServerVersion)
	if err != nil || server.AtLeast(floor) {
		return nil
	}
	return []Finding{{
		ID:          "MGS007",
		Severity:    SeverityError,
		Title:       "ComputeClass objects exist below the minimum GKE version",
		Resource:    "Cluster",
		Detail:      fmt.Sprintf("Custom ComputeClasses require %s; this control plane is %s.", req.Min, ctx.Snap.ServerVersion),
		Remediation: "Upgrade the control plane to " + req.Min + " or later.",
		Doc:         req.Doc,
	}}
}

// --- MGS008 --------------------------------------------------------------

func checkNodePoolAutoCreationNAP(ctx Context) []Finding {
	req := ctx.Cat.Require("node_pool_auto_creation")
	floor := gkeversion.MustParse(req.Min)
	server, err := gkeversion.Parse(ctx.Snap.ServerVersion)
	if err != nil || server.AtLeast(floor) {
		return nil
	}
	var out []Finding
	for _, cc := range ctx.Snap.ComputeClasses {
		if !cc.Spec.NodePoolAutoCreation.Enabled {
			continue
		}
		out = append(out, Finding{
			ID:       "MGS008",
			Severity: SeverityWarn,
			Title:    "nodePoolAutoCreation needs cluster-level node auto-provisioning on this version",
			Resource: "ComputeClass/" + cc.Name,
			Detail: fmt.Sprintf("This control plane is %s, below %s. On earlier versions "+
				"nodePoolAutoCreation.enabled additionally requires cluster-level node auto-provisioning. "+
				"If NAP is off, node pools are simply never created and there is no error explaining why. "+
				"This tool cannot read the NAP setting from the Kubernetes API - verify it directly.",
				ctx.Snap.ServerVersion, req.Min),
			Remediation: "Run: gcloud container clusters describe CLUSTER --format='value(autoscaling.enableNodeAutoprovisioning)' " +
				"and enable it, or upgrade the control plane to " + req.Min + " or later.",
			Doc: req.Doc,
		})
	}
	return out
}

// --- MGS103 --------------------------------------------------------------

func checkStatefulSetPinned(ctx Context) []Finding {
	f := classify(ctx)
	if !f.isMixed() {
		return nil
	}
	var out []Finding
	for _, w := range ctx.Snap.Workloads {
		if w.Kind != "StatefulSet" || len(w.VCTs) == 0 {
			continue
		}
		var names []string
		for _, vct := range w.VCTs {
			names = append(names, vct.ClaimName)
		}
		out = append(out, Finding{
			ID:       "MGS103",
			Severity: SeverityWarn,
			Title:    "StatefulSet volumes are pinned to the generation that first provisioned them",
			Resource: fmt.Sprintf("StatefulSet/%s/%s", w.Namespace, w.Name),
			Detail: fmt.Sprintf("volumeClaimTemplates (%s) create durable per-replica volumes. Automated disk "+
				"type selection runs once, at first provision, so each volume keeps its disk type for life. "+
				"On this mixed fleet those replicas cannot move across the Persistent Disk / Hyperdisk divide.",
				strings.Join(names, ", ")),
			Remediation: "Accept the pinning and set use-allowed-disk-topology so scheduling respects it, or " +
				"plan a snapshot-and-restore migration onto the target disk type.",
			Doc: docHyperdisk,
		})
	}
	return out
}

// --- MGS104 --------------------------------------------------------------

func checkLocalSSDLoss(ctx Context) []Finding {
	// Which families currently in the fleet carry Local SSD?
	withLocalSSD := map[string]bool{}
	for _, n := range ctx.Snap.Nodes {
		if n.LocalSSDCount > 0 && n.Family != "" {
			withLocalSSD[n.Family] = true
		}
	}
	if len(withLocalSSD) == 0 {
		return nil
	}

	var out []Finding
	for _, cc := range ctx.Snap.ComputeClasses {
		var lacking []string
		for _, p := range cc.Spec.Priorities {
			name := p.MachineFamily
			if name == "" {
				name = catalog.FamilyOfMachineType(p.MachineType)
			}
			fam, ok := ctx.Cat.Family(name)
			if !ok || fam.LocalSSD {
				continue
			}
			lacking = append(lacking, name)
		}
		if len(lacking) == 0 {
			continue
		}
		sort.Strings(lacking)
		var have []string
		for fam := range withLocalSSD {
			have = append(have, fam)
		}
		sort.Strings(have)
		out = append(out, Finding{
			ID:       "MGS104",
			Severity: SeverityWarn,
			Title:    "ComputeClass can place Pods on families without Local SSD",
			Resource: "ComputeClass/" + cc.Name,
			Detail: fmt.Sprintf("Node pools using Local SSD today: %s. This ComputeClass can also provision %s, "+
				"which support no Local SSD at all. Workloads relying on a Local SSD emptyDir for scratch space "+
				"will silently fall back to the node boot disk, or fail to schedule, depending on how they ask for it.",
				strings.Join(have, ", "), strings.Join(lacking, ", ")),
			Remediation: "Move scratch storage onto a generic ephemeral volume backed by a type: dynamic " +
				"StorageClass so it works on every family in the class.",
			Doc: "https://cloud.google.com/compute/docs/disks/local-ssd",
		})
	}
	return out
}

// --- MGS106 --------------------------------------------------------------

func checkScaleUpAnyway(ctx Context) []Finding {
	f := classify(ctx)
	var out []Finding
	for _, cc := range ctx.Snap.ComputeClasses {
		if !strings.EqualFold(cc.Spec.WhenUnsatisfiable, "ScaleUpAnyway") {
			continue
		}
		detail := "whenUnsatisfiable: ScaleUpAnyway lets GKE provision nodes using cluster defaults when no " +
			"priority rule can be satisfied - including from a machine family you never listed."
		if f.isMixed() {
			detail += fmt.Sprintf(" On this mixed fleet (%s alongside %s) that can reintroduce a node whose "+
				"disk capabilities you have not vetted.",
				strings.Join(f.hyperdiskOnly, "/"), strings.Join(append(f.pdOnly, f.both...), "/"))
		}
		out = append(out, Finding{
			ID:          "MGS106",
			Severity:    SeverityWarn,
			Title:       "ComputeClass allows scale-up outside its priority rules",
			Resource:    "ComputeClass/" + cc.Name,
			Detail:      detail,
			Remediation: "Prefer whenUnsatisfiable: DoNotScaleUp on a mixed fleet. A Pending Pod with a clear reason beats a running node with an unattachable disk.",
			Doc:         docComputeClass,
		})
	}
	return out
}

// --- MGS107 --------------------------------------------------------------

func checkActiveMigration(ctx Context) []Finding {
	boundPD := 0
	for _, v := range ctx.Snap.Volumes {
		if catalog.IsPersistentDiskType(v.DiskType) && v.ClaimName != "" {
			boundPD++
		}
	}
	if boundPD == 0 {
		return nil
	}
	f := classify(ctx)
	if len(f.hyperdiskOnly) == 0 {
		return nil
	}

	var out []Finding
	for _, cc := range ctx.Snap.ComputeClasses {
		if !cc.Spec.ActiveMigration.OptimizeRulePriority {
			continue
		}
		out = append(out, Finding{
			ID:       "MGS107",
			Severity: SeverityWarn,
			Title:    "Active migration can move Pods onto nodes their bound volume cannot follow",
			Resource: "ComputeClass/" + cc.Name,
			Detail: fmt.Sprintf("activeMigration.optimizeRulePriority moves Pods back to higher-priority rules "+
				"when that capacity returns. There are %d bound Persistent Disk volume(s) in this cluster and "+
				"%s node pools that cannot attach Persistent Disk. Migrating one of those Pods upward is a "+
				"scheduled outage.", boundPD, strings.Join(f.hyperdiskOnly, "/")),
			Remediation: "Set use-allowed-disk-topology on the StorageClasses backing those volumes, or restrict " +
				"active migration to workloads using generic ephemeral volumes.",
			Doc: docComputeClass,
		})
	}
	return out
}
