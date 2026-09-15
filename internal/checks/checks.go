// Package checks holds the audit rules. Each rule maps to a specific,
// documented GKE behaviour; see docs/CHECKS.md for the prose version.
package checks

import (
	"sort"

	"github.com/igofman/gke-mixed-generation-storage/internal/catalog"
	"github.com/igofman/gke-mixed-generation-storage/internal/cluster"
)

// Severity of a finding.
type Severity string

const (
	// SeverityError marks something that will fail, or already is failing.
	SeverityError Severity = "error"
	// SeverityWarn marks a latent hazard or a cost surprise.
	SeverityWarn Severity = "warn"
	// SeverityInfo is context, never a failure.
	SeverityInfo Severity = "info"
)

// Rank orders severities for sorting and for --fail-on comparisons.
func (s Severity) Rank() int {
	switch s {
	case SeverityError:
		return 2
	case SeverityWarn:
		return 1
	default:
		return 0
	}
}

// Finding is one result.
type Finding struct {
	ID          string   `json:"id"`
	Severity    Severity `json:"severity"`
	Title       string   `json:"title"`
	Resource    string   `json:"resource,omitempty"`
	Detail      string   `json:"detail"`
	Remediation string   `json:"remediation,omitempty"`
	Doc         string   `json:"doc,omitempty"`
}

// Options tune the run.
type Options struct {
	// Per-region monthly prices, supplied by the user. Zero means "do not
	// estimate cost" - we refuse to invent prices that vary by region.
	PriceThroughputMiBPerMonth float64
	PriceIOPSPerMonth          float64
	// Skip lists check IDs to suppress.
	Skip map[string]bool
}

// Context is what every check receives.
type Context struct {
	Cat  *catalog.Catalog
	Snap *cluster.Snapshot
	Opts Options
}

// Check is one audit rule.
type Check struct {
	ID   string
	Name string
	Run  func(Context) []Finding
}

// All returns every registered check, in stable ID order.
func All() []Check {
	all := []Check{
		{ID: "MGS001", Name: "pd-volume-reachable-by-hyperdisk-only-nodes", Run: checkPDVolumeOnHyperdiskOnlyFleet},
		{ID: "MGS002", Name: "shared-rwo-volume-multi-replica", Run: checkSharedRWOMultiReplica},
		{ID: "MGS003", Name: "computeclass-boot-disk-mismatch", Run: checkComputeClassBootDisk},
		{ID: "MGS004", Name: "dynamic-storageclass-version-floor", Run: checkDynamicVersionFloor},
		{ID: "MGS005", Name: "allowed-disk-topology-node-pool-floor", Run: checkTopologyNodePoolFloor},
		{ID: "MGS006", Name: "pd-csi-driver-missing", Run: checkCSIDriver},
		{ID: "MGS007", Name: "computeclass-version-floor", Run: checkComputeClassVersionFloor},
		{ID: "MGS008", Name: "node-pool-auto-creation-requires-nap", Run: checkNodePoolAutoCreationNAP},
		{ID: "MGS100", Name: "dynamic-missing-disk-type-preference", Run: checkMissingDiskTypePreference},
		{ID: "MGS101", Name: "dynamic-missing-allowed-disk-topology", Run: checkMissingAllowedDiskTopology},
		{ID: "MGS102", Name: "hyperdisk-provisioned-above-free-baseline", Run: checkAboveBaseline},
		{ID: "MGS103", Name: "statefulset-volume-generation-pinned", Run: checkStatefulSetPinned},
		{ID: "MGS104", Name: "local-ssd-capability-loss-on-fallback", Run: checkLocalSSDLoss},
		{ID: "MGS105", Name: "default-storageclass-not-mixed-safe", Run: checkDefaultStorageClass},
		{ID: "MGS106", Name: "computeclass-scale-up-anyway", Run: checkScaleUpAnyway},
		{ID: "MGS107", Name: "active-migration-with-bound-pd-volumes", Run: checkActiveMigration},
		{ID: "MGS108", Name: "unknown-machine-family", Run: checkUnknownFamilies},
		{ID: "MGS200", Name: "fleet-summary", Run: checkFleetSummary},
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all
}

// Run executes every check that has not been skipped.
func Run(ctx Context) []Finding {
	var out []Finding
	for _, c := range All() {
		if ctx.Opts.Skip[c.ID] {
			continue
		}
		out = append(out, c.Run(ctx)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity.Rank() != out[j].Severity.Rank() {
			return out[i].Severity.Rank() > out[j].Severity.Rank()
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Highest returns the most severe severity present, or SeverityInfo.
func Highest(fs []Finding) Severity {
	worst := SeverityInfo
	for _, f := range fs {
		if f.Severity.Rank() > worst.Rank() {
			worst = f.Severity
		}
	}
	return worst
}
