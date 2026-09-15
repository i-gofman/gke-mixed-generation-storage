// Package catalog holds the two pieces of Google Cloud reference data the
// checker needs: minimum GKE versions and the machine-family block storage
// capability matrix. Both are embedded YAML so they can be corrected without
// touching any logic.
package catalog

import (
	_ "embed"
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

//go:embed versions.yaml
var versionsYAML []byte

//go:embed machine-families.yaml
var familiesYAML []byte

// Requirement is one minimum-version floor.
type Requirement struct {
	ID        string   `json:"id"`
	Min       string   `json:"min"`
	AppliesTo []string `json:"applies_to"`
	Summary   string   `json:"summary"`
	Doc       string   `json:"doc"`
}

// AppliesToNodePools reports whether node pools, and not just the control
// plane, must meet this floor.
func (r Requirement) AppliesToNodePools() bool {
	for _, a := range r.AppliesTo {
		if a == "node_pools" {
			return true
		}
	}
	return false
}

// Baseline is the included (free) performance of a Hyperdisk Balanced volume.
type Baseline struct {
	IOPS              int    `json:"iops"`
	ThroughputMiBPerS int    `json:"throughput_mib_per_sec"`
	Doc               string `json:"doc"`
}

type versionsFile struct {
	Requirements map[string]Requirement `json:"requirements"`
	Baseline     Baseline               `json:"hyperdisk_balanced_baseline"`
}

// Family describes what block storage a machine family can attach.
type Family struct {
	Name                 string   `json:"-"`
	Generation           int      `json:"generation"`
	PersistentDisk       []string `json:"persistent_disk"`
	Hyperdisk            []string `json:"hyperdisk"`
	HyperdiskAllowlisted []string `json:"hyperdisk_allowlisted"`
	LocalSSD             bool     `json:"local_ssd"`
	BootDisk             []string `json:"boot_disk"`
	Notes                string   `json:"notes"`
}

// SupportsPersistentDisk reports whether the family can attach any PD type.
func (f Family) SupportsPersistentDisk() bool { return len(f.PersistentDisk) > 0 }

// SupportsHyperdisk reports whether the family can attach any Hyperdisk type.
func (f Family) SupportsHyperdisk() bool { return len(f.Hyperdisk) > 0 }

// SupportsBoth reports whether the family is one of the "supports both"
// families that disk-type-preference arbitrates between.
func (f Family) SupportsBoth() bool { return f.SupportsPersistentDisk() && f.SupportsHyperdisk() }

// Supports reports whether the family can attach the named disk type.
func (f Family) Supports(diskType string) bool {
	for _, t := range append(append([]string{}, f.PersistentDisk...), f.Hyperdisk...) {
		if t == diskType {
			return true
		}
	}
	return false
}

// IsAllowlisted reports whether the named type requires an account-team
// request on this family.
func (f Family) IsAllowlisted(diskType string) bool {
	for _, t := range f.HyperdiskAllowlisted {
		if t == diskType {
			return true
		}
	}
	return false
}

type familiesFile struct {
	Families map[string]Family `json:"families"`
}

// Catalog is the loaded reference data.
type Catalog struct {
	Requirements map[string]Requirement
	Baseline     Baseline
	Families     map[string]Family
}

// Load parses the embedded reference data.
func Load() (*Catalog, error) {
	var vf versionsFile
	if err := yaml.Unmarshal(versionsYAML, &vf); err != nil {
		return nil, fmt.Errorf("parsing versions.yaml: %w", err)
	}
	var ff familiesFile
	if err := yaml.Unmarshal(familiesYAML, &ff); err != nil {
		return nil, fmt.Errorf("parsing machine-families.yaml: %w", err)
	}
	for name, f := range ff.Families {
		f.Name = name
		ff.Families[name] = f
	}
	return &Catalog{Requirements: vf.Requirements, Baseline: vf.Baseline, Families: ff.Families}, nil
}

// Require returns a named requirement, panicking if the embedded data has
// drifted from the code. Both are in this repo, so a mismatch is a build bug.
func (c *Catalog) Require(id string) Requirement {
	r, ok := c.Requirements[id]
	if !ok {
		panic("unknown requirement id: " + id)
	}
	return r
}

// Family looks up a machine family. The boolean reports whether the family is
// known; unknown families are reported to the user rather than assumed safe.
func (c *Catalog) Family(name string) (Family, bool) {
	f, ok := c.Families[strings.ToLower(name)]
	return f, ok
}

// FamilyOfMachineType extracts the family from a machine type such as
// "n4-standard-4" or "c4a-highmem-8-lssd".
func FamilyOfMachineType(machineType string) string {
	if machineType == "" {
		return ""
	}
	if i := strings.Index(machineType, "-"); i > 0 {
		return strings.ToLower(machineType[:i])
	}
	return strings.ToLower(machineType)
}

// IsPersistentDiskType reports whether a disk type string is a PD SKU.
func IsPersistentDiskType(t string) bool { return strings.HasPrefix(t, "pd-") }

// IsHyperdiskType reports whether a disk type string is a Hyperdisk SKU.
func IsHyperdiskType(t string) bool { return strings.HasPrefix(t, "hyperdisk-") }
