package cluster

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func fakeClients(t *testing.T, serverVersion string, objs []runtime.Object, computeClasses []runtime.Object) (*fake.Clientset, *dynamicfake.FakeDynamicClient) {
	t.Helper()

	kc := fake.NewClientset(objs...)
	disc, ok := kc.Discovery().(*fakediscovery.FakeDiscovery)
	if !ok {
		t.Fatal("fake clientset did not return a FakeDiscovery")
	}
	disc.FakedServerVersion = &version.Info{GitVersion: serverVersion}

	scheme := runtime.NewScheme()
	dc := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{ComputeClassGVR: "ComputeClassList"},
		computeClasses...,
	)
	return kc, dc
}

func TestCollectNodesAndVolumes(t *testing.T) {
	objs := []runtime.Object{
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gke-n4-abc",
				Labels: map[string]string{
					"node.kubernetes.io/instance-type": "n4-standard-4",
					"cloud.google.com/gke-nodepool":    "pool-n4",
				},
			},
			Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.35.3-gke.1290000"}},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gke-n2-def",
				Labels: map[string]string{
					"node.kubernetes.io/instance-type":     "n2-standard-8",
					"cloud.google.com/gke-nodepool":        "pool-n2",
					"cloud.google.com/gke-local-ssd-count": "2",
				},
			},
			Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.34.1-gke.2541000"}},
		},
		&storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: "standard-rwo", Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}},
			Provisioner: PDCSIDriver,
			Parameters:  map[string]string{"type": "pd-balanced"},
		},
		&storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: PDCSIDriver}},
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-from-attributes"},
			Spec: corev1.PersistentVolumeSpec{
				StorageClassName: "standard-rwo",
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				ClaimRef:         &corev1.ObjectReference{Namespace: "db", Name: "data-0"},
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:           PDCSIDriver,
						VolumeAttributes: map[string]string{"type": "hyperdisk-balanced"},
					},
				},
			},
		},
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-from-storageclass"},
			Spec: corev1.PersistentVolumeSpec{
				StorageClassName: "standard-rwo",
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{Driver: PDCSIDriver},
				},
			},
		},
	}

	kc, dc := fakeClients(t, "v1.35.3-gke.1290000", objs, nil)
	snap, err := Collect(context.Background(), kc, dc, "")
	if err != nil {
		t.Fatalf("Collect(): %v", err)
	}

	if snap.ServerVersion != "v1.35.3-gke.1290000" {
		t.Errorf("ServerVersion = %q", snap.ServerVersion)
	}
	if len(snap.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(snap.Nodes))
	}
	byName := map[string]Node{}
	for _, n := range snap.Nodes {
		byName[n.Name] = n
	}
	if got := byName["gke-n4-abc"].Family; got != "n4" {
		t.Errorf("family derived from machine type = %q, want n4", got)
	}
	if got := byName["gke-n2-def"].LocalSSDCount; got != 2 {
		t.Errorf("LocalSSDCount = %d, want 2", got)
	}
	if got := snap.Families(); len(got) != 2 {
		t.Errorf("Families() = %v, want two entries", got)
	}

	if !snap.PDCSIInstalled {
		t.Error("PDCSIInstalled = false, want true")
	}
	if snap.DefaultSCName != "standard-rwo" {
		t.Errorf("DefaultSCName = %q", snap.DefaultSCName)
	}

	// Disk type provenance matters: the report says where the value came from,
	// so getting these backwards would make findings misleading.
	vols := map[string]Volume{}
	for _, v := range snap.Volumes {
		vols[v.PVName] = v
	}
	if v := vols["pvc-from-attributes"]; v.DiskType != "hyperdisk-balanced" || v.DiskTypeFrom != "PV volumeAttributes" {
		t.Errorf("attributes volume = %q from %q, want hyperdisk-balanced from PV volumeAttributes", v.DiskType, v.DiskTypeFrom)
	}
	if v := vols["pvc-from-attributes"]; v.ClaimNS != "db" || v.ClaimName != "data-0" {
		t.Errorf("claim ref = %s/%s, want db/data-0", v.ClaimNS, v.ClaimName)
	}
	if v := vols["pvc-from-storageclass"]; v.DiskType != "pd-balanced" || v.DiskTypeFrom != "StorageClass parameters" {
		t.Errorf("fallback volume = %q from %q, want pd-balanced from StorageClass parameters", v.DiskType, v.DiskTypeFrom)
	}
}

// A dynamic StorageClass carries no concrete type, so a PV that does not
// report one must stay unknown rather than being labelled "dynamic".
func TestCollectDoesNotTreatDynamicAsADiskType(t *testing.T) {
	objs := []runtime.Object{
		&storagev1.StorageClass{
			ObjectMeta:  metav1.ObjectMeta{Name: "dyn"},
			Provisioner: PDCSIDriver,
			Parameters:  map[string]string{"type": "dynamic"},
		},
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{Name: "pvc-dyn"},
			Spec: corev1.PersistentVolumeSpec{
				StorageClassName: "dyn",
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{Driver: PDCSIDriver},
				},
			},
		},
	}

	kc, dc := fakeClients(t, "v1.35.3-gke.1290000", objs, nil)
	snap, err := Collect(context.Background(), kc, dc, "")
	if err != nil {
		t.Fatalf("Collect(): %v", err)
	}
	if len(snap.Volumes) != 1 {
		t.Fatalf("got %d volumes, want 1", len(snap.Volumes))
	}
	if got := snap.Volumes[0].DiskType; got != "" {
		t.Errorf("DiskType = %q, want empty (unknown)", got)
	}
}

func TestCollectComputeClasses(t *testing.T) {
	cc := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cloud.google.com/v1",
		"kind":       "ComputeClass",
		"metadata":   map[string]any{"name": "n4-preferred"},
		"spec": map[string]any{
			"priorities": []any{
				map[string]any{
					"machineFamily": "n4",
					"storage":       map[string]any{"bootDiskType": "hyperdisk-balanced"},
				},
				map[string]any{
					"machineFamily": "n2",
					"storage":       map[string]any{"bootDiskType": "pd-balanced", "localSSDCount": int64(2)},
				},
			},
			"whenUnsatisfiable":    "ScaleUpAnyway",
			"nodePoolAutoCreation": map[string]any{"enabled": true},
			"activeMigration":      map[string]any{"optimizeRulePriority": true},
		},
	}}

	kc, dc := fakeClients(t, "v1.35.3-gke.1290000", nil, []runtime.Object{cc})
	snap, err := Collect(context.Background(), kc, dc, "")
	if err != nil {
		t.Fatalf("Collect(): %v", err)
	}

	if !snap.ComputeClassCRD {
		t.Error("ComputeClassCRD = false, want true")
	}
	if len(snap.ComputeClasses) != 1 {
		t.Fatalf("got %d ComputeClasses, want 1", len(snap.ComputeClasses))
	}
	got := snap.ComputeClasses[0]
	if got.Name != "n4-preferred" {
		t.Errorf("Name = %q", got.Name)
	}
	if len(got.Spec.Priorities) != 2 {
		t.Fatalf("got %d priorities, want 2", len(got.Spec.Priorities))
	}
	if got.Spec.Priorities[0].Storage.BootDiskType != "hyperdisk-balanced" {
		t.Errorf("priority 0 bootDiskType = %q", got.Spec.Priorities[0].Storage.BootDiskType)
	}
	if got.Spec.Priorities[1].Storage.LocalSSDCount != 2 {
		t.Errorf("priority 1 localSSDCount = %d, want 2", got.Spec.Priorities[1].Storage.LocalSSDCount)
	}
	if got.Spec.WhenUnsatisfiable != "ScaleUpAnyway" {
		t.Errorf("WhenUnsatisfiable = %q", got.Spec.WhenUnsatisfiable)
	}
	if !got.Spec.NodePoolAutoCreation.Enabled || !got.Spec.ActiveMigration.OptimizeRulePriority {
		t.Errorf("nested spec booleans lost: %+v", got.Spec)
	}
}

func TestFamilyOfMachineTypeViaNodeLabelOverride(t *testing.T) {
	objs := []runtime.Object{&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "custom",
			Labels: map[string]string{
				"node.kubernetes.io/instance-type": "custom-8-16384",
				"cloud.google.com/machine-family":  "n2",
			},
		},
	}}
	kc, dc := fakeClients(t, "v1.35.3-gke.1290000", objs, nil)
	snap, err := Collect(context.Background(), kc, dc, "")
	if err != nil {
		t.Fatalf("Collect(): %v", err)
	}
	if got := snap.Nodes[0].Family; got != "n2" {
		t.Errorf("Family = %q, want n2 from the explicit label", got)
	}
}
