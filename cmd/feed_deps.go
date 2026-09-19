package cmd

import (
	"fmt"
	"os"

	"github.com/mwtrigg/nugctl/internal/deps"
	"github.com/mwtrigg/nugctl/internal/output"
	"github.com/spf13/cobra"
)

var feedDepsCmd = &cobra.Command{
	Use:   "deps",
	Short: "Check whether a package's declared dependencies resolve in the feed",
	Long: `Walk package versions in the feed and check each declared NuGet
dependency (id + version range) against what the feed actually has listed.
Designed to run in CI: exit 0 means every dependency resolved, exit 1 means
at least one is missing or unsatisfied, exit 2 means the scan couldn't even
run (e.g. the service index was unreachable, or --package names a package
the feed doesn't have).

A 429 or 503 response is retried a few times with backoff (honoring the
feed's Retry-After header, if it sends one) before giving up. Use --max-rps
to cap the request rate up front for a feed sitting behind a rate limiter
(e.g. nginx limit_req) that would otherwise start rejecting requests
partway through a scan.`,
	Example: `  nugctl feed deps
  nugctl feed deps --package Newtonsoft.Json
  nugctl feed deps -o json
  nugctl feed deps --max-rps 5`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pkg, _ := cmd.Flags().GetString("package")
		maxRPS, _ := cmd.Flags().GetFloat64("max-rps")

		c, err := resolveClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(2)
		}

		report := deps.Run(c, deps.Options{Package: pkg, MaxRPS: maxRPS})
		printDepsReport(report)
		os.Exit(report.ExitCode())
		return nil
	},
}

func printDepsReport(r *deps.Report) {
	switch effectiveFormat() {
	case output.FormatJSON:
		output.PrintJSON(r)
		return
	case output.FormatYAML:
		output.PrintYAML(r)
		return
	}

	if r.Aborted {
		fmt.Printf("ABORTED: %s\n\n", r.AbortReason)
	}
	ok, missing, unsatisfied := r.Counts()
	fmt.Printf("Feed: %s\n", r.FeedURL)
	fmt.Printf("%d ok, %d missing, %d unsatisfied\n\n", ok, missing, unsatisfied)

	headers := []string{"PACKAGE", "VERSION", "DEPENDENCY", "RANGE", "STATUS", "DETAIL"}
	rows := make([][]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		rows = append(rows, []string{f.Package, f.Version, f.DependencyID, f.Range, string(f.Status), f.Detail})
	}
	output.PrintTable(headers, rows)
}

func init() {
	feedDepsCmd.Flags().String("package", "", "package ID to scan (default: every package in the feed)")
	feedDepsCmd.Flags().Float64("max-rps", 0, "cap requests per second against the feed (default: unlimited); a single 429/503 is retried a few times with backoff regardless")
	feedCmd.AddCommand(feedDepsCmd)
}
