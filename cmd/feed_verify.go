package cmd

import (
	"fmt"
	"os"

	"github.com/mwtrigg/nugctl/internal/output"
	"github.com/mwtrigg/nugctl/internal/verify"
	"github.com/spf13/cobra"
)

var feedVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Validate a feed's NuGet v3 conformance",
	Long: `Run read-only, negative, and (with --push) round-trip checks against a
feed to validate it implements the NuGet v3 protocol correctly. Designed to
run in CI: exit 0 means every executed check passed, exit 1 means at least
one check failed, exit 2 means verify couldn't even run (e.g. the service
index was unreachable).`,
	Example: `  nugctl feed verify
  nugctl feed verify --package Newtonsoft.Json
  nugctl feed verify --push -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		push, _ := cmd.Flags().GetBool("push")
		pkg, _ := cmd.Flags().GetString("package")
		forceDelete, _ := cmd.Flags().GetBool("force-delete")

		if forceDelete && !push {
			return fmt.Errorf("--force-delete requires --push")
		}

		c, err := resolveClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(2)
		}

		report := verify.Run(c, verify.Options{Push: push, Package: pkg, ForceDelete: forceDelete})
		printVerifyReport(report)
		os.Exit(report.ExitCode())
		return nil
	},
}

func printVerifyReport(r *verify.Report) {
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
	pass, fail, warn, skip := r.Counts()
	fmt.Printf("Feed: %s\n", r.FeedURL)
	fmt.Printf("%d passed, %d failed, %d warned, %d skipped\n\n", pass, fail, warn, skip)

	headers := []string{"CATEGORY", "CHECK", "STATUS", "DETAIL"}
	rows := make([][]string, 0, len(r.Checks))
	for _, chk := range r.Checks {
		detail := chk.Detail
		if chk.Err != "" {
			if detail != "" {
				detail += ": "
			}
			detail += chk.Err
		}
		rows = append(rows, []string{chk.Category, chk.Name, string(chk.Status), detail})
	}
	output.PrintTable(headers, rows)
}

func init() {
	feedVerifyCmd.Flags().Bool("push", false, "also run round-trip checks (push, poll, download, unlist) against a writable feed")
	feedVerifyCmd.Flags().String("package", "", "package ID to use for integrity checks (default: first hit of an unfiltered search)")
	feedVerifyCmd.Flags().Bool("force-delete", false, "append force=true to the round-trip's cleanup delete, for feeds (e.g. Barn) that reject deleting a recently-downloaded package with 409 otherwise; feed-specific extension, requires --push")
	feedCmd.AddCommand(feedVerifyCmd)
}
