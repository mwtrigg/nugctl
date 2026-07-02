package cmd

import (
	"fmt"

	"github.com/mwtrigg/nugctl/internal/client"
	"github.com/spf13/cobra"
)

var packageDeprecateCmd = &cobra.Command{
	Use:   "deprecate <id>",
	Short: "Mark a package version as deprecated",
	Long: `Mark a package version as deprecated on the feed.

Sends PUT .../deprecations per the NuGet.org deprecation API. Reason flags are
additive — combine as needed:

  --legacy           Package is superseded; use --alternate-package to point elsewhere
  --critical-bugs    Package has critical bugs
  --other            Other reason; use --message to explain

If the server does not implement the deprecation endpoint (BaGetter does not),
a clear error is shown rather than a generic failure.

To clear deprecation use: nugctl package undeprecate <id> --version <ver>`,
	Example: `  nugctl package deprecate MyLib --version 1.0.0 --legacy --alternate-package MyLib.V2
  nugctl package deprecate MyLib --version 1.0.0 --critical-bugs --message "see CVE-2026-1234"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		version, _ := cmd.Flags().GetString("version")
		force, _ := cmd.Flags().GetBool("force")
		legacy, _ := cmd.Flags().GetBool("legacy")
		criticalBugs, _ := cmd.Flags().GetBool("critical-bugs")
		other, _ := cmd.Flags().GetBool("other")
		message, _ := cmd.Flags().GetString("message")
		altPkg, _ := cmd.Flags().GetString("alternate-package")
		altVer, _ := cmd.Flags().GetString("alternate-version")

		if !legacy && !criticalBugs && !other {
			return fmt.Errorf("at least one reason required: --legacy, --critical-bugs, or --other")
		}

		if !force {
			fmt.Printf("Deprecate %s %s? [y/N] ", id, version)
			var resp string
			fmt.Scanln(&resp)
			if resp != "y" && resp != "Y" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		c, err := resolveClient()
		if err != nil {
			return err
		}

		req := client.DeprecationRequest{
			Versions:                []string{version},
			IsLegacy:                legacy,
			HasCriticalBugs:         criticalBugs,
			IsOther:                 other,
			Message:                 message,
			AlternatePackageID:      altPkg,
			AlternatePackageVersion: altVer,
		}

		if err := c.Deprecate(id, version, req); err != nil {
			if client.IsNotSupported(err) {
				return fmt.Errorf("%w\nhint: deprecation is available on NuGet.org and Azure Artifacts but not BaGetter by default", err)
			}
			return err
		}

		fmt.Printf("Package %s %s marked as deprecated.\n", id, version)
		return nil
	},
}

var packageUndeprecateCmd = &cobra.Command{
	Use:   "undeprecate <id>",
	Short: "Clear deprecation from a package version",
	Long: `Clear the deprecation status from a package version.

Sends a PUT with an empty deprecation body, which clears any existing
deprecation markers on NuGet-compatible feeds that support the API.

If the server does not implement the deprecation endpoint, a clear error
is shown rather than a generic failure.`,
	Example: `  nugctl package undeprecate MyLib --version 1.0.0`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		version, _ := cmd.Flags().GetString("version")

		c, err := resolveClient()
		if err != nil {
			return err
		}

		if err := c.Undeprecate(id, version); err != nil {
			if client.IsNotSupported(err) {
				return fmt.Errorf("%w\nhint: deprecation is available on NuGet.org and Azure Artifacts but not BaGetter by default", err)
			}
			return err
		}

		fmt.Printf("Deprecation cleared for %s %s.\n", id, version)
		return nil
	},
}

func init() {
	packageDeprecateCmd.Flags().String("version", "", "version to deprecate")
	packageDeprecateCmd.Flags().Bool("force", false, "skip confirmation prompt")
	packageDeprecateCmd.Flags().Bool("legacy", false, "package is superseded/legacy")
	packageDeprecateCmd.Flags().Bool("critical-bugs", false, "package has critical bugs")
	packageDeprecateCmd.Flags().Bool("other", false, "other reason (use --message to explain)")
	packageDeprecateCmd.Flags().String("message", "", "deprecation message shown to users")
	packageDeprecateCmd.Flags().String("alternate-package", "", "ID of the recommended replacement package")
	packageDeprecateCmd.Flags().String("alternate-version", "", "version of the recommended replacement package")
	packageDeprecateCmd.MarkFlagRequired("version")

	packageUndeprecateCmd.Flags().String("version", "", "version to undeprecate")
	packageUndeprecateCmd.MarkFlagRequired("version")

	packageCmd.AddCommand(packageDeprecateCmd)
	packageCmd.AddCommand(packageUndeprecateCmd)
}
