package checks

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/igofman/gke-mixed-generation-storage/internal/catalog"
	"github.com/igofman/gke-mixed-generation-storage/internal/cluster"
)

func testContext(t *testing.T, snap *cluster.Snapshot) Context {
	t.Helper()
	cat, err := catalog.Load()
	if err != nil {
		t.Fatalf("catalog.Load(): %v", err)
	}
	return Context{Cat: cat, Snap: snap, Opts: Options{Skip: map[string]bool{}}}
}

func node(name, pool, machineType, kubelet string) cluster.Node {
	return cluster.Node{
		Name:        name,
		NodePool:    pool,
		MachineType: machineType,
		Family:      catalog.FamilyOfMachineType(machineType),
		KubeletVer:  kubelet,
	}
}

func storageClass(name string, params map[string]string) storagev1.StorageClass {
	return storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: name},
		Provisioner: cluster.PDCSIDriver,
		Parameters:  params,
	}
}

// ids returns the finding IDs, so tests assert on behaviour rather than prose.
func ids(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.ID)
	}
	return out
}

func has(fs []Finding, id string) bool {
	for _, f := range fs {
		if f.ID == id {
			return true
		}
	}
	return false
}

// The headline case from the article: a pd-balanced volume bound while the Pod
// ran on N2, in a cluster that has since grown N4 node pools.
func TestMixedFleetWithBoundPDVolume(t *testing.T) {
	snap := &cluster.Snapshot{
		ServerVersion: "1.35.3-gke.1290000",
		Nodes: []cluster.Node{
			node("n2-a", "pool-n2", "n2-standard-4", "1.35.3-gke.1290000"),
			node("n4-a", "pool-n4", "n4-standard-4", "1.35.3-gke.1290000"),
		},
		StorageClasses: []storagev1.StorageClass{
			storageClass("standard-rwo", map[string]string{"type": "pd-balanced"}),
		},
		DefaultSCName: "standard-rwo",
		Volumes: []cluster.Volume{{
			PVName:       "pvc-1",
			ClaimNS:      "default",
			ClaimName:    "data",
			StorageClass: "standard-rwo",
			DiskType:     "pd-balanced",
			DiskTypeFrom: "PV csi.volumeAttributes.type",
			AccessModes:  []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			IsCSI:        true,
		}},
		PDCSIInstalled: true,
	}

	got := Run(testContext(t, snap))
	if !has(got, "MGS001") {
		t.Errorf("want MGS001 for a PD volume in a fleet containing N4, got %v", ids(got))
	}
	if !has(got, "MGS105") {
		t.Errorf("want MGS105 for a PD-provisioning default StorageClass, got %v", ids(got))
	}
	if Highest(got) != SeverityError {
		t.Errorf("Highest() = %q, want error", Highest(got))
	}
}

// use-allowed-disk-topology is the supported way to keep a bound PD volume
// away from N4, so MGS001 must stand down when it is set.
func TestTopologyParameterSuppressesMGS001(t *testing.T) {
	snap := &cluster.Snapshot{
		ServerVersion: "1.35.3-gke.1290000",
		Nodes: []cluster.Node{
			node("n2-a", "pool-n2", "n2-standard-4", "1.35.3-gke.1290000"),
			node("n4-a", "pool-n4", "n4-standard-4", "1.35.3-gke.1290000"),
		},
		StorageClasses: []storagev1.StorageClass{
			storageClass("mixed-safe", map[string]string{
				"type":                      "dynamic",
				"disk-type-preference":      "hyperdisk-type",
				"use-allowed-disk-topology": "true",
			}),
		},
		Volumes: []cluster.Volume{{
			PVName: "pvc-1", ClaimNS: "default", ClaimName: "data",
			StorageClass: "mixed-safe", DiskType: "pd-balanced",
			DiskTypeFrom: "PV csi.volumeAttributes.type", IsCSI: true,
		}},
		PDCSIInstalled: true,
	}

	got := Run(testContext(t, snap))
	if has(got, "MGS001") {
		t.Errorf("MGS001 should be suppressed when use-allowed-disk-topology is set, got %v", ids(got))
	}
	if has(got, "MGS100") || has(got, "MGS101") {
		t.Errorf("a fully configured dynamic StorageClass should raise neither MGS100 nor MGS101, got %v", ids(got))
	}
}

// The floor for use-allowed-disk-topology applies to node pools as well as the
// control plane; a lagging pool is the failure mode that looks like "Pending
// for no reason".
func TestTopologyFloorCatchesLaggingNodePool(t *testing.T) {
	snap := &cluster.Snapshot{
		ServerVersion: "1.35.3-gke.1290000",
		Nodes: []cluster.Node{
			node("n2-a", "pool-n2", "n2-standard-4", "1.33.4-gke.9999999"),
			node("n4-a", "pool-n4", "n4-standard-4", "1.35.3-gke.1290000"),
		},
		StorageClasses: []storagev1.StorageClass{
			storageClass("mixed-safe", map[string]string{
				"type": "dynamic", "use-allowed-disk-topology": "true",
				"disk-type-preference": "hyperdisk-type",
			}),
		},
		PDCSIInstalled: true,
	}

	got := Run(testContext(t, snap))
	if !has(got, "MGS005") {
		t.Fatalf("want MGS005 for a node pool below the topology floor, got %v", ids(got))
	}
	for _, f := range got {
		if f.ID == "MGS005" && !strings.Contains(f.Detail, "pool-n2") {
			t.Errorf("MGS005 should name the lagging pool, got %q", f.Detail)
		}
	}
}

// A single-generation fleet should stay quiet apart from the summary.
func TestHomogeneousFleetIsQuiet(t *testing.T) {
	snap := &cluster.Snapshot{
		ServerVersion: "1.35.3-gke.1290000",
		Nodes: []cluster.Node{
			node("n2-a", "pool-n2", "n2-standard-4", "1.35.3-gke.1290000"),
			node("n2-b", "pool-n2", "n2-standard-4", "1.35.3-gke.1290000"),
		},
		StorageClasses: []storagev1.StorageClass{
			storageClass("standard-rwo", map[string]string{"type": "pd-balanced"}),
		},
		DefaultSCName: "standard-rwo",
		Volumes: []cluster.Volume{{
			PVName: "pvc-1", ClaimNS: "default", ClaimName: "data",
			StorageClass: "standard-rwo", DiskType: "pd-balanced",
			DiskTypeFrom: "PV csi.volumeAttributes.type", IsCSI: true,
		}},
		PDCSIInstalled: true,
	}

	got := Run(testContext(t, snap))
	if Highest(got) != SeverityInfo {
		t.Errorf("a homogeneous N2 fleet should produce no errors or warnings, got %v", ids(got))
	}
}

// MGS004 fires only below the automated-selection floor.
func TestDynamicVersionFloor(t *testing.T) {
	mk := func(serverVersion string) []Finding {
		return Run(testContext(t, &cluster.Snapshot{
			ServerVersion: serverVersion,
			Nodes:         []cluster.Node{node("n4-a", "pool-n4", "n4-standard-4", serverVersion)},
			StorageClasses: []storagev1.StorageClass{
				storageClass("dyn", map[string]string{"type": "dynamic", "disk-type-preference": "hyperdisk-type"}),
			},
			PDCSIInstalled: true,
		}))
	}
	if !has(mk("1.34.1-gke.2541000"), "MGS004") {
		t.Error("want MGS004 below the automated disk type selection floor")
	}
	if has(mk("1.35.3-gke.1290000"), "MGS004") {
		t.Error("MGS004 should not fire at the floor version")
	}
}

// A ComputeClass that asks for a boot disk its machine family cannot use will
// never bring up a node.
func TestComputeClassBootDiskMismatch(t *testing.T) {
	cc := cluster.ComputeClass{Name: "mixed"}
	p := cluster.ComputeClassPriority{MachineFamily: "n4"}
	p.Storage.BootDiskType = "pd-balanced"
	cc.Spec.Priorities = []cluster.ComputeClassPriority{p}

	snap := &cluster.Snapshot{
		ServerVersion:   "1.35.3-gke.1290000",
		Nodes:           []cluster.Node{node("n4-a", "pool-n4", "n4-standard-4", "1.35.3-gke.1290000")},
		ComputeClasses:  []cluster.ComputeClass{cc},
		ComputeClassCRD: true,
		PDCSIInstalled:  true,
	}

	got := Run(testContext(t, snap))
	if !has(got, "MGS003") {
		t.Errorf("want MGS003 for an N4 priority with a pd-balanced boot disk, got %v", ids(got))
	}
}

// --skip must suppress a check that would otherwise fire.
func TestSkipSuppressesCheck(t *testing.T) {
	snap := &cluster.Snapshot{
		ServerVersion:  "1.35.3-gke.1290000",
		Nodes:          []cluster.Node{node("n9-a", "pool-x", "n9-standard-4", "1.35.3-gke.1290000")},
		PDCSIInstalled: true,
	}
	ctx := testContext(t, snap)
	if !has(Run(ctx), "MGS108") {
		t.Fatalf("want MGS108 for an unknown machine family, got %v", ids(Run(ctx)))
	}
	ctx.Opts.Skip = map[string]bool{"MGS108": true}
	if has(Run(ctx), "MGS108") {
		t.Error("MGS108 should be suppressed when skipped")
	}
}
