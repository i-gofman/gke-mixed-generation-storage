package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/igofman/gke-mixed-generation-storage/internal/checks"
)

func sampleDoc() Document {
	findings := []checks.Finding{
		{
			ID: "MGS001", Severity: checks.SeverityError,
			Title:       "Persistent Disk volume can be scheduled onto a Hyperdisk-only node",
			Resource:    "PersistentVolume/pvc-1 (claim db/data)",
			Detail:      "This volume is pd-balanced via StorageClass \"standard-rwo\".",
			Remediation: "Set use-allowed-disk-topology on the StorageClass.",
			Doc:         "https://example.invalid/hyperdisk",
		},
		{
			ID: "MGS102", Severity: checks.SeverityWarn,
			Title:    "Hyperdisk performance provisioned above the free baseline",
			Resource: "StorageClass/fast",
			Detail:   "12000 IOPS provisioned, 9000 above the free baseline of 3000.",
		},
		{ID: "MGS200", Severity: checks.SeverityInfo, Title: "Fleet summary", Resource: "Cluster", Detail: "2 nodes."},
	}
	return Document{
		Tool: "mixed-fleet-check", Version: "test", Cluster: "1.35.3-gke.1290000",
		Summary: Summarise(findings), Findings: findings,
	}
}

func TestSummarise(t *testing.T) {
	got := sampleDoc().Summary
	want := Summary{Errors: 1, Warns: 1, Infos: 1}
	if got != want {
		t.Errorf("Summarise() = %+v, want %+v", got, want)
	}
}

func TestTableIncludesRemediation(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, sampleDoc(), false); err != nil {
		t.Fatalf("Table(): %v", err)
	}
	out := buf.String()
	for _, want := range []string{"MGS001", "ERROR", "use-allowed-disk-topology", "1 error(s), 1 warning(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := Table(&buf, Document{Tool: "t"}, false); err != nil {
		t.Fatalf("Table(): %v", err)
	}
	if !strings.Contains(buf.String(), "No findings.") {
		t.Errorf("want a clean-run message, got %q", buf.String())
	}
}

func TestJSONRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, sampleDoc()); err != nil {
		t.Fatalf("JSON(): %v", err)
	}
	var back Document
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Findings) != 3 || back.Findings[0].ID != "MGS001" {
		t.Errorf("round trip lost findings: %+v", back.Findings)
	}
	if back.Summary.Errors != 1 {
		t.Errorf("Summary.Errors = %d, want 1", back.Summary.Errors)
	}
}

// SARIF is consumed by GitHub code scanning, so the level mapping and the
// deduplicated rule list are the parts that have to be right.
func TestSARIFShape(t *testing.T) {
	var buf bytes.Buffer
	if err := SARIF(&buf, sampleDoc()); err != nil {
		t.Fatalf("SARIF(): %v", err)
	}

	var log struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name  string `json:"name"`
					Rules []struct {
						ID string `json:"id"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID string `json:"ruleId"`
				Level  string `json:"level"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(buf.Bytes(), &log); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if log.Version != "2.1.0" {
		t.Errorf("version = %q, want 2.1.0", log.Version)
	}
	if len(log.Runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(log.Runs))
	}
	run := log.Runs[0]
	if len(run.Results) != 3 {
		t.Errorf("got %d results, want 3", len(run.Results))
	}
	if len(run.Tool.Driver.Rules) != 3 {
		t.Errorf("got %d rules, want 3 (one per distinct ID)", len(run.Tool.Driver.Rules))
	}

	wantLevels := map[string]string{"MGS001": "error", "MGS102": "warning", "MGS200": "note"}
	for _, r := range run.Results {
		if want := wantLevels[r.RuleID]; r.Level != want {
			t.Errorf("%s level = %q, want %q", r.RuleID, r.Level, want)
		}
	}
}

// Repeated IDs must collapse into one rule entry, or code scanning rejects the
// upload.
func TestSARIFDeduplicatesRules(t *testing.T) {
	doc := Document{Tool: "t", Findings: []checks.Finding{
		{ID: "MGS001", Severity: checks.SeverityError, Title: "a", Detail: "x"},
		{ID: "MGS001", Severity: checks.SeverityError, Title: "a", Detail: "y"},
	}}
	var buf bytes.Buffer
	if err := SARIF(&buf, doc); err != nil {
		t.Fatalf("SARIF(): %v", err)
	}
	if n := strings.Count(buf.String(), `"shortDescription"`); n != 1 {
		t.Errorf("got %d rule entries for one repeated ID, want 1", n)
	}
}

// Width counts the text only, not the continuation indent, so "four five" (9
// chars) still fits a width of 9 on its own line.
func TestWrapIndentsContinuations(t *testing.T) {
	got := wrap("one two three four five", 9, "| ")
	want := "one two\n| three\n| four five"
	if got != want {
		t.Errorf("wrap() = %q, want %q", got, want)
	}
}

func TestWrapEmpty(t *testing.T) {
	if got := wrap("   ", 20, "  "); got != "" {
		t.Errorf("wrap(whitespace) = %q, want empty", got)
	}
}
