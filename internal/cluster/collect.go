// Package cluster reads a read-only snapshot of everything the checks need.
// Nothing in this package mutates cluster state.
package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/igofman/gke-mixed-generation-storage/internal/catalog"
)

// PDCSIDriver is the Compute Engine persistent disk CSI driver name.
const PDCSIDriver = "pd.csi.storage.gke.io"

// ComputeClassGVR is the custom ComputeClass resource.
var ComputeClassGVR = schema.GroupVersionResource{
	Group:    "cloud.google.com",
	Version:  "v1",
	Resource: "computeclasses",
}

// Node is the subset of a node the checks care about.
type Node struct {
	Name          string
	NodePool      string
	MachineType   string
	Family        string
	KubeletVer    string
	LocalSSDCount int
	ComputeClass  string
	Unschedulable bool
}

// Volume pairs a PersistentVolume with the claim and disk type behind it.
type Volume struct {
	PVName       string
	ClaimNS      string
	ClaimName    string
	StorageClass string
	DiskType     string // resolved where possible, "" when unknown
	DiskTypeFrom string // how DiskType was determined, for honest reporting
	AccessModes  []corev1.PersistentVolumeAccessMode
	IsCSI        bool
}

// VolumeRef is one volume mounted by a workload.
type VolumeRef struct {
	ClaimName    string // empty for generic ephemeral volumes
	StorageClass string // set for generic ephemeral volumes and VCTs
	Ephemeral    bool
}

// Workload is a Deployment or StatefulSet, flattened.
type Workload struct {
	Kind         string
	Namespace    string
	Name         string
	Replicas     int32
	NodeSelector map[string]string
	Volumes      []VolumeRef
	VCTs         []VolumeRef // StatefulSet volumeClaimTemplates
}

// ComputeClassPriority is one entry in spec.priorities.
type ComputeClassPriority struct {
	MachineFamily string `json:"machineFamily,omitempty"`
	MachineType   string `json:"machineType,omitempty"`
	Spot          bool   `json:"spot,omitempty"`
	Storage       struct {
		BootDiskType  string `json:"bootDiskType,omitempty"`
		BootDiskSize  int    `json:"bootDiskSize,omitempty"`
		LocalSSDCount int    `json:"localSSDCount,omitempty"`
	} `json:"storage,omitempty"`
}

// ComputeClass is the subset of the CRD the checks use.
type ComputeClass struct {
	Name string `json:"-"`
	Spec struct {
		Priorities           []ComputeClassPriority `json:"priorities,omitempty"`
		WhenUnsatisfiable    string                 `json:"whenUnsatisfiable,omitempty"`
		NodePoolAutoCreation struct {
			Enabled bool `json:"enabled,omitempty"`
		} `json:"nodePoolAutoCreation,omitempty"`
		ActiveMigration struct {
			OptimizeRulePriority bool `json:"optimizeRulePriority,omitempty"`
		} `json:"activeMigration,omitempty"`
	} `json:"spec"`
}

// Snapshot is everything the checks run against.
type Snapshot struct {
	ServerVersion   string
	Nodes           []Node
	StorageClasses  []storagev1.StorageClass
	DefaultSCName   string
	Volumes         []Volume
	Workloads       []Workload
	ComputeClasses  []ComputeClass
	PDCSIInstalled  bool
	ComputeClassCRD bool

	// Warnings records things we could not read (usually RBAC) so the report
	// can say "not checked" instead of silently passing.
	Warnings []string
}

// Collect gathers the snapshot. namespace == "" means all namespaces.
func Collect(ctx context.Context, kc kubernetes.Interface, dc dynamic.Interface, namespace string) (*Snapshot, error) {
	s := &Snapshot{}

	sv, err := kc.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("reading server version: %w", err)
	}
	s.ServerVersion = sv.GitVersion

	nodes, err := kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	for _, n := range nodes.Items {
		machineType := n.Labels["node.kubernetes.io/instance-type"]
		family := n.Labels["cloud.google.com/machine-family"]
		if family == "" {
			family = catalog.FamilyOfMachineType(machineType)
		}
		count := 0
		if v := n.Labels["cloud.google.com/gke-local-ssd-count"]; v != "" {
			count, _ = strconv.Atoi(v)
		}
		s.Nodes = append(s.Nodes, Node{
			Name:          n.Name,
			NodePool:      n.Labels["cloud.google.com/gke-nodepool"],
			MachineType:   machineType,
			Family:        family,
			KubeletVer:    n.Status.NodeInfo.KubeletVersion,
			LocalSSDCount: count,
			ComputeClass:  n.Labels["cloud.google.com/compute-class"],
			Unschedulable: n.Spec.Unschedulable,
		})
	}

	scs, err := kc.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing storageclasses: %w", err)
	}
	s.StorageClasses = scs.Items
	scByName := map[string]storagev1.StorageClass{}
	for _, sc := range scs.Items {
		scByName[sc.Name] = sc
		if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			s.DefaultSCName = sc.Name
		}
	}

	if drivers, err := kc.StorageV1().CSIDrivers().List(ctx, metav1.ListOptions{}); err == nil {
		for _, d := range drivers.Items {
			if d.Name == PDCSIDriver {
				s.PDCSIInstalled = true
			}
		}
	} else {
		s.Warnings = append(s.Warnings, fmt.Sprintf("could not list csidrivers (%v); CSI driver check skipped", err))
		s.PDCSIInstalled = true // do not raise a false alarm on a permissions gap
	}

	pvs, err := kc.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		s.Warnings = append(s.Warnings, fmt.Sprintf("could not list persistentvolumes (%v); bound-volume checks skipped", err))
	} else {
		for _, pv := range pvs.Items {
			v := Volume{
				PVName:       pv.Name,
				StorageClass: pv.Spec.StorageClassName,
				AccessModes:  pv.Spec.AccessModes,
			}
			if pv.Spec.ClaimRef != nil {
				v.ClaimNS, v.ClaimName = pv.Spec.ClaimRef.Namespace, pv.Spec.ClaimRef.Name
			}
			if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == PDCSIDriver {
				v.IsCSI = true
				if t := pv.Spec.CSI.VolumeAttributes["type"]; t != "" {
					v.DiskType, v.DiskTypeFrom = t, "PV volumeAttributes"
				} else if sc, ok := scByName[pv.Spec.StorageClassName]; ok {
					if t := sc.Parameters["type"]; t != "" && t != "dynamic" {
						v.DiskType, v.DiskTypeFrom = t, "StorageClass parameters"
					}
				}
			}
			s.Volumes = append(s.Volumes, v)
		}
	}

	ns := namespace
	deps, err := kc.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		s.Warnings = append(s.Warnings, fmt.Sprintf("could not list deployments (%v); workload checks skipped", err))
	} else {
		for _, d := range deps.Items {
			w := Workload{
				Kind: "Deployment", Namespace: d.Namespace, Name: d.Name,
				Replicas:     ptrInt32(d.Spec.Replicas),
				NodeSelector: d.Spec.Template.Spec.NodeSelector,
				Volumes:      podVolumes(d.Spec.Template.Spec.Volumes),
			}
			s.Workloads = append(s.Workloads, w)
		}
	}

	sts, err := kc.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		s.Warnings = append(s.Warnings, fmt.Sprintf("could not list statefulsets (%v)", err))
	} else {
		for _, st := range sts.Items {
			w := Workload{
				Kind: "StatefulSet", Namespace: st.Namespace, Name: st.Name,
				Replicas:     ptrInt32(st.Spec.Replicas),
				NodeSelector: st.Spec.Template.Spec.NodeSelector,
				Volumes:      podVolumes(st.Spec.Template.Spec.Volumes),
			}
			for _, vct := range st.Spec.VolumeClaimTemplates {
				ref := VolumeRef{ClaimName: vct.Name}
				if vct.Spec.StorageClassName != nil {
					ref.StorageClass = *vct.Spec.StorageClassName
				}
				w.VCTs = append(w.VCTs, ref)
			}
			s.Workloads = append(s.Workloads, w)
		}
	}

	ccs, err := dc.Resource(ComputeClassGVR).List(ctx, metav1.ListOptions{})
	switch {
	case apierrors.IsNotFound(err), meta_IsNoMatch(err):
		// ComputeClass CRD absent: a valid state, not an error.
	case err != nil:
		s.Warnings = append(s.Warnings, fmt.Sprintf("could not list computeclasses (%v); ComputeClass checks skipped", err))
	default:
		s.ComputeClassCRD = true
		for _, item := range ccs.Items {
			raw, mErr := item.MarshalJSON()
			if mErr != nil {
				continue
			}
			var cc ComputeClass
			if jErr := json.Unmarshal(raw, &cc); jErr != nil {
				s.Warnings = append(s.Warnings, fmt.Sprintf("could not parse ComputeClass %s: %v", item.GetName(), jErr))
				continue
			}
			cc.Name = item.GetName()
			s.ComputeClasses = append(s.ComputeClasses, cc)
		}
	}

	return s, nil
}

// Families returns the distinct machine families present in the cluster.
func (s *Snapshot) Families() []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range s.Nodes {
		if n.Family != "" && !seen[n.Family] {
			seen[n.Family] = true
			out = append(out, n.Family)
		}
	}
	return out
}

// StorageClass returns a StorageClass by name.
func (s *Snapshot) StorageClass(name string) (storagev1.StorageClass, bool) {
	for _, sc := range s.StorageClasses {
		if sc.Name == name {
			return sc, true
		}
	}
	return storagev1.StorageClass{}, false
}

func podVolumes(vols []corev1.Volume) []VolumeRef {
	var out []VolumeRef
	for _, v := range vols {
		switch {
		case v.PersistentVolumeClaim != nil:
			out = append(out, VolumeRef{ClaimName: v.PersistentVolumeClaim.ClaimName})
		case v.Ephemeral != nil && v.Ephemeral.VolumeClaimTemplate != nil:
			ref := VolumeRef{Ephemeral: true}
			if sc := v.Ephemeral.VolumeClaimTemplate.Spec.StorageClassName; sc != nil {
				ref.StorageClass = *sc
			}
			out = append(out, ref)
		}
	}
	return out
}

func ptrInt32(p *int32) int32 {
	if p == nil {
		return 1
	}
	return *p
}

// meta_IsNoMatch reports a "no matches for kind" error from the RESTMapper or
// the API server, which is how an absent CRD surfaces through the dynamic
// client on some paths.
func meta_IsNoMatch(err error) bool {
	if err == nil {
		return false
	}
	return apierrors.IsNotFound(err) ||
		apierrors.IsMethodNotSupported(err) ||
		containsNoMatch(err.Error())
}

func containsNoMatch(s string) bool {
	for _, probe := range []string{"no matches for kind", "could not find the requested resource", "the server could not find the requested resource"} {
		if len(s) >= len(probe) && indexOf(s, probe) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
