// Command mixed-fleet-check audits a GKE cluster for storage problems caused by
// mixing machine generations that do not share a disk type (for example N2 and
// N4). It is read-only: it never writes to the cluster.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/igofman/gke-mixed-generation-storage/internal/catalog"
	"github.com/igofman/gke-mixed-generation-storage/internal/checks"
	"github.com/igofman/gke-mixed-generation-storage/internal/cluster"
	"github.com/igofman/gke-mixed-generation-storage/internal/report"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// Exit codes. Anything above 2 means the tool itself failed, not the cluster.
const (
	exitOK      = 0
	exitWarn    = 1
	exitError   = 2
	exitRuntime = 3
)

type options struct {
	kubeconfig      string
	kubeContext     string
	namespace       string
	format          string
	failOn          string
	skip            []string
	priceThroughput float64
	priceIOPS       float64
	verbose         bool
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitRuntime)
	}
}

func newRootCmd() *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "mixed-fleet-check",
		Short: "Audit a GKE cluster for mixed-generation (N2/N4) storage hazards",
		Long: `mixed-fleet-check inspects a GKE cluster read-only and reports storage
configurations that break when nodes of different machine generations coexist.

N4-family nodes cannot attach Persistent Disk at all, so a pd-balanced volume
bound while a Pod ran on N2 will fail to attach if that Pod is ever rescheduled
onto N4. The checks find those volumes, plus the version floors, StorageClass
parameters and ComputeClass settings that decide whether the cluster recovers
on its own.

Exit codes: 0 clean, 1 warnings, 2 errors, 3 the tool failed to run.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			code, err := run(c.Context(), opts, c.OutOrStdout())
			if err != nil {
				return err
			}
			if code != exitOK {
				os.Exit(code)
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.kubeconfig, "kubeconfig", "", "path to kubeconfig (default: $KUBECONFIG, then ~/.kube/config)")
	f.StringVar(&opts.kubeContext, "context", "", "kubeconfig context to use")
	f.StringVarP(&opts.namespace, "namespace", "n", "", "limit workload inspection to one namespace (default: all)")
	f.StringVarP(&opts.format, "format", "o", "table", "output format: table, json, sarif")
	f.StringVar(&opts.failOn, "fail-on", "error", "minimum severity that sets a non-zero exit code: error, warn, none")
	f.StringSliceVar(&opts.skip, "skip", nil, "check IDs to suppress, e.g. --skip MGS102,MGS108")
	f.Float64Var(&opts.priceThroughput, "price-throughput-mibps-month", 0,
		"your region's price per provisioned MiB/s per month; enables cost estimates in MGS102")
	f.Float64Var(&opts.priceIOPS, "price-iops-month", 0,
		"your region's price per provisioned IOPS per month; enables cost estimates in MGS102")
	f.BoolVarP(&opts.verbose, "verbose", "v", false, "expand informational findings in table output")

	cmd.AddCommand(newVersionCmd(), newListChecksCmd())
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fmt.Fprintln(c.OutOrStdout(), version)
			return nil
		},
	}
}

func newListChecksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-checks",
		Short: "List every check ID this build knows about",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			for _, ch := range checks.All() {
				fmt.Fprintf(c.OutOrStdout(), "%s  %s\n", ch.ID, ch.Name)
			}
			return nil
		},
	}
}

// run performs the audit and returns the process exit code.
func run(ctx context.Context, opts *options, out io.Writer) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	failOn, err := parseFailOn(opts.failOn)
	if err != nil {
		return exitRuntime, err
	}
	if opts.format != "table" && opts.format != "json" && opts.format != "sarif" {
		return exitRuntime, fmt.Errorf("unknown --format %q: want table, json or sarif", opts.format)
	}

	cat, err := catalog.Load()
	if err != nil {
		return exitRuntime, fmt.Errorf("loading catalog: %w", err)
	}

	restCfg, err := restConfig(opts.kubeconfig, opts.kubeContext)
	if err != nil {
		return exitRuntime, fmt.Errorf("building client config: %w", err)
	}
	kc, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return exitRuntime, fmt.Errorf("building kubernetes client: %w", err)
	}
	dc, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return exitRuntime, fmt.Errorf("building dynamic client: %w", err)
	}

	snap, err := cluster.Collect(ctx, kc, dc, opts.namespace)
	if err != nil {
		return exitRuntime, fmt.Errorf("collecting cluster state: %w", err)
	}

	skip := map[string]bool{}
	for _, id := range opts.skip {
		id = strings.ToUpper(strings.TrimSpace(id))
		if id != "" {
			skip[id] = true
		}
	}

	findings := checks.Run(checks.Context{
		Cat:  cat,
		Snap: snap,
		Opts: checks.Options{
			PriceThroughputMiBPerMonth: opts.priceThroughput,
			PriceIOPSPerMonth:          opts.priceIOPS,
			Skip:                       skip,
		},
	})

	doc := report.Document{
		Tool:     "mixed-fleet-check",
		Version:  version,
		Cluster:  snap.ServerVersion,
		Summary:  report.Summarise(findings),
		Findings: findings,
		Warnings: snap.Warnings,
	}

	switch opts.format {
	case "json":
		err = report.JSON(out, doc)
	case "sarif":
		err = report.SARIF(out, doc)
	default:
		err = report.Table(out, doc, opts.verbose)
	}
	if err != nil {
		return exitRuntime, err
	}

	worst := checks.Highest(findings)
	if failOn == nil || worst.Rank() < failOn.Rank() {
		return exitOK, nil
	}
	if worst == checks.SeverityError {
		return exitError, nil
	}
	return exitWarn, nil
}

// parseFailOn returns nil for "none", meaning always exit 0.
func parseFailOn(s string) (*checks.Severity, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none", "":
		return nil, nil
	case "warn", "warning":
		sev := checks.SeverityWarn
		return &sev, nil
	case "error":
		sev := checks.SeverityError
		return &sev, nil
	default:
		return nil, fmt.Errorf("unknown --fail-on %q: want error, warn or none", s)
	}
}

func restConfig(kubeconfig, kubeContext string) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
}
