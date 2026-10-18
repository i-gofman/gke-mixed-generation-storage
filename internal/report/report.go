// Package report renders findings as a terminal table, JSON, or SARIF.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/igofman/gke-mixed-generation-storage/internal/checks"
)

// Summary counts findings by severity.
type Summary struct {
	Errors int `json:"errors"`
	Warns  int `json:"warnings"`
	Infos  int `json:"info"`
}

// Document is the JSON output shape.
type Document struct {
	Tool     string           `json:"tool"`
	Version  string           `json:"version"`
	Cluster  string           `json:"clusterVersion"`
	Summary  Summary          `json:"summary"`
	Findings []checks.Finding `json:"findings"`
	Warnings []string         `json:"collectionWarnings,omitempty"`
}

// Summarise counts findings.
func Summarise(fs []checks.Finding) Summary {
	var s Summary
	for _, f := range fs {
		switch f.Severity {
		case checks.SeverityError:
			s.Errors++
		case checks.SeverityWarn:
			s.Warns++
		default:
			s.Infos++
		}
	}
	return s
}

// Table writes a human-readable report.
func Table(w io.Writer, doc Document, verbose bool) error {
	if len(doc.Findings) == 0 {
		fmt.Fprintln(w, "No findings.")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SEVERITY\tID\tRESOURCE\tFINDING")
	for _, f := range doc.Findings {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", strings.ToUpper(string(f.Severity)), f.ID, truncate(f.Resource, 46), f.Title)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w)
	for _, f := range doc.Findings {
		if f.Severity == checks.SeverityInfo && !verbose {
			fmt.Fprintf(w, "%s  %s\n         %s\n\n", f.ID, f.Title, wrap(f.Detail, 88, "         "))
			continue
		}
		fmt.Fprintf(w, "%s [%s]  %s\n", f.ID, strings.ToUpper(string(f.Severity)), f.Title)
		if f.Resource != "" {
			fmt.Fprintf(w, "  resource:    %s\n", f.Resource)
		}
		fmt.Fprintf(w, "  detail:      %s\n", wrap(f.Detail, 88, "               "))
		if f.Remediation != "" {
			fmt.Fprintf(w, "  fix:         %s\n", wrap(f.Remediation, 88, "               "))
		}
		if f.Doc != "" {
			fmt.Fprintf(w, "  docs:        %s\n", f.Doc)
		}
		fmt.Fprintln(w)
	}

	for _, warn := range doc.Warnings {
		fmt.Fprintf(w, "note: %s\n", warn)
	}
	fmt.Fprintf(w, "%d error(s), %d warning(s), %d informational.\n",
		doc.Summary.Errors, doc.Summary.Warns, doc.Summary.Infos)
	return nil
}

// JSON writes the machine-readable report.
func JSON(w io.Writer, doc Document) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// SARIF writes a SARIF 2.1.0 log so the report can be uploaded to GitHub code
// scanning from a CI job.
func SARIF(w io.Writer, doc Document) error {
	type sarifMessage struct {
		Text string `json:"text"`
	}
	type sarifRule struct {
		ID               string       `json:"id"`
		ShortDescription sarifMessage `json:"shortDescription"`
		HelpURI          string       `json:"helpUri,omitempty"`
	}
	type sarifResult struct {
		RuleID  string       `json:"ruleId"`
		Level   string       `json:"level"`
		Message sarifMessage `json:"message"`
	}

	seen := map[string]bool{}
	var rules []sarifRule
	var results []sarifResult
	for _, f := range doc.Findings {
		if !seen[f.ID] {
			seen[f.ID] = true
			rules = append(rules, sarifRule{ID: f.ID, ShortDescription: sarifMessage{Text: f.Title}, HelpURI: f.Doc})
		}
		level := "note"
		switch f.Severity {
		case checks.SeverityError:
			level = "error"
		case checks.SeverityWarn:
			level = "warning"
		}
		msg := f.Detail
		if f.Resource != "" {
			msg = f.Resource + ": " + msg
		}
		if f.Remediation != "" {
			msg += " Fix: " + f.Remediation
		}
		results = append(results, sarifResult{RuleID: f.ID, Level: level, Message: sarifMessage{Text: msg}})
	}

	log := map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs": []any{map[string]any{
			"tool": map[string]any{"driver": map[string]any{
				"name":           doc.Tool,
				"version":        doc.Version,
				"informationUri": "https://github.com/igofman/gke-mixed-generation-storage",
				"rules":          rules,
			}},
			"results": results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// wrap hard-wraps text at width, indenting continuation lines.
func wrap(s string, width int, indent string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for i, word := range words {
		if i > 0 {
			if lineLen+1+len(word) > width {
				b.WriteString("\n" + indent)
				lineLen = 0
			} else {
				b.WriteString(" ")
				lineLen++
			}
		}
		b.WriteString(word)
		lineLen += len(word)
	}
	return b.String()
}
