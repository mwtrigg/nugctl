package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mwtrigg/nugctl/internal/output"
	"github.com/spf13/cobra"
)

var packageCmd = &cobra.Command{
	Use:     "package",
	Aliases: []string{"pkg", "nupkg"},
	Short:   "Manage NuGet packages",
}

// searchProperties are valid --properties values for package search.
var searchProperties = []string{"authors", "tags", "totaldownloads", "verified", "projecturl"}

var packageSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search for packages",
	Example: `  nugctl package search newtonsoft
  nugctl package search json --take 50 --prerelease
  nugctl package search log --properties authors,tags -o json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		q := ""
		if len(args) > 0 {
			q = args[0]
		}
		skip, _ := cmd.Flags().GetInt("skip")
		take, _ := cmd.Flags().GetInt("take")
		prerelease, _ := cmd.Flags().GetBool("prerelease")

		c, err := resolveClient()
		if err != nil {
			return err
		}
		result, err := c.Search(q, skip, take, prerelease)
		if err != nil {
			return err
		}

		switch effectiveFormat() {
		case output.FormatJSON:
			return output.PrintJSON(result)
		case output.FormatYAML:
			return output.PrintYAML(result)
		default:
			headers := []string{"ID", "VERSION", "DESCRIPTION"}
			if hasProperty("authors") {
				headers = append(headers, "AUTHORS")
			}
			if hasProperty("tags") {
				headers = append(headers, "TAGS")
			}
			if hasProperty("totaldownloads") {
				headers = append(headers, "DOWNLOADS")
			}
			if hasProperty("verified") {
				headers = append(headers, "VERIFIED")
			}
			if hasProperty("projecturl") {
				headers = append(headers, "PROJECT URL")
			}

			rows := make([][]string, 0, len(result.Data))
			for _, p := range result.Data {
				desc := p.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				row := []string{p.ID, p.Version, desc}
				if hasProperty("authors") {
					row = append(row, strings.Join(p.Authors, ", "))
				}
				if hasProperty("tags") {
					row = append(row, strings.Join(p.Tags, ", "))
				}
				if hasProperty("totaldownloads") {
					row = append(row, strconv.Itoa(p.TotalDownloads))
				}
				if hasProperty("verified") {
					row = append(row, strconv.FormatBool(p.Verified))
				}
				if hasProperty("projecturl") {
					row = append(row, p.ProjectURL)
				}
				rows = append(rows, row)
			}
			fmt.Printf("Total hits: %d\n\n", result.TotalHits)
			output.PrintTable(headers, rows)
		}
		return nil
	},
}

// listProperties are valid --properties values for package list.
var listProperties = []string{"published", "downloads", "prerelease"}

var packageListCmd = &cobra.Command{
	Use:   "list --id <id>",
	Short: "List all versions of a package",
	Example: `  nugctl package list --id Newtonsoft.Json
  nugctl package list Newtonsoft.Json --all-properties`,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, _ := cmd.Flags().GetString("id")
		if id == "" && len(args) > 0 {
			id = args[0]
		}
		if id == "" {
			return fmt.Errorf("--id required")
		}

		c, err := resolveClient()
		if err != nil {
			return err
		}
		idx, err := c.Registration(id)
		if err != nil {
			return err
		}

		type row struct {
			ID           string
			Version      string
			Published    string
			Downloads    string
			IsPrerelease string
		}

		var entries []row
		for _, page := range idx.Items {
			for _, leaf := range page.Items {
				e := leaf.CatalogEntry
				r := row{
					ID:           e.ID,
					Version:      e.Version,
					Published:    e.Published,
					IsPrerelease: strconv.FormatBool(e.IsPrerelease),
				}
				entries = append(entries, r)
			}
		}

		switch effectiveFormat() {
		case output.FormatJSON:
			return output.PrintJSON(entries)
		case output.FormatYAML:
			return output.PrintYAML(entries)
		default:
			headers := []string{"ID", "VERSION"}
			if hasProperty("published") || flagAllProperties {
				headers = append(headers, "PUBLISHED")
			}
			if hasProperty("prerelease") || flagAllProperties {
				headers = append(headers, "PRE-RELEASE")
			}
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				r := []string{e.ID, e.Version}
				if hasProperty("published") || flagAllProperties {
					r = append(r, e.Published)
				}
				if hasProperty("prerelease") || flagAllProperties {
					r = append(r, e.IsPrerelease)
				}
				rows = append(rows, r)
			}
			output.PrintTable(headers, rows)
		}
		return nil
	},
}

// infoProperties are valid --properties values for package info.
var infoProperties = []string{
	"description", "authors", "tags", "published", "projecturl", "licenseurl",
	"summary", "title", "packagehash", "packagesize", "listed",
}

var packageInfoCmd = &cobra.Command{
	Use:   "info <id>",
	Short: "Show package metadata",
	Example: `  nugctl package info Newtonsoft.Json
  nugctl package info Newtonsoft.Json --version 13.0.4`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		version, _ := cmd.Flags().GetString("version")

		c, err := resolveClient()
		if err != nil {
			return err
		}

		var entry interface{}
		if version != "" {
			e, err := c.RegistrationVersion(id, version)
			if err != nil {
				return err
			}
			entry = e
		} else {
			idx, err := c.Registration(id)
			if err != nil {
				return err
			}
			// latest = last item in last page
			if idx.Count == 0 || len(idx.Items) == 0 {
				return fmt.Errorf("package %q not found", id)
			}
			page := idx.Items[len(idx.Items)-1]
			if len(page.Items) == 0 {
				return fmt.Errorf("package %q has no versions", id)
			}
			entry = &page.Items[len(page.Items)-1].CatalogEntry
		}

		switch effectiveFormat() {
		case output.FormatJSON:
			return output.PrintJSON(entry)
		case output.FormatYAML:
			return output.PrintYAML(entry)
		default:
			// Use reflection-free approach: marshal to map then print
			return output.PrintJSON(entry)
		}
	},
}

var packagePushCmd = &cobra.Command{
	Use:     "push <file.nupkg>",
	Short:   "Push a package to the feed",
	Example: `  nugctl package push ./bin/MyLib.1.2.3.nupkg`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		c, err := resolveClient()
		if err != nil {
			return err
		}
		fmt.Printf("Pushing %s...\n", path)
		if err := c.Push(path); err != nil {
			return err
		}
		fmt.Println("Package pushed successfully.")
		return nil
	},
}

var packagePullCmd = &cobra.Command{
	Use:     "pull <id>",
	Short:   "Download a package from the feed",
	Example: `  nugctl package pull Newtonsoft.Json --version 13.0.4 --output-dir ./out`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		version, _ := cmd.Flags().GetString("version")
		if version == "" {
			return fmt.Errorf("--version required")
		}
		outDir, _ := cmd.Flags().GetString("output-dir")
		if outDir == "" {
			outDir = "."
		}

		c, err := resolveClient()
		if err != nil {
			return err
		}
		fmt.Printf("Pulling %s %s...\n", id, version)
		outFile, err := c.Pull(id, version, outDir)
		if err != nil {
			return err
		}
		fmt.Printf("Saved to %s\n", outFile)
		return nil
	},
}

var packageUnlistCmd = &cobra.Command{
	Use:     "unlist <id>",
	Aliases: []string{"delete"},
	Short:   "Unlist (or hard-delete) a package version from the feed",
	Long: `Unlist a package version from the feed.

By default this sends DELETE /api/v2/package/{id}/{version}, which the NuGet v3
spec defines as an unlist operation: the package is hidden from search results
but remains downloadable by direct URL. This is BaGetter's default behavior
(PackageDeletionBehavior: Unlist).

Use --hard to signal intent for a hard delete. Whether the server honors this
depends on its configuration (PackageDeletionBehavior: HardDelete). The same
HTTP call is made either way; --hard is advisory and affects messaging only.

NuGet deprecation (marking a package as legacy/buggy with an alternate pointer)
is a separate operation not yet supported by BaGetter.`,
	Example: `  nugctl package unlist Newtonsoft.Json --version 13.0.4
  nugctl package unlist Newtonsoft.Json --version 13.0.4 --hard --force`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		version, _ := cmd.Flags().GetString("version")
		if version == "" {
			return fmt.Errorf("--version required")
		}
		force, _ := cmd.Flags().GetBool("force")
		hard, _ := cmd.Flags().GetBool("hard")

		action := "unlist"
		if hard {
			action = "hard-delete"
		}

		if !force {
			fmt.Printf("About to %s %s %s. This cannot be undone. Confirm? [y/N] ", action, id, version)
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
		fmt.Printf("%s %s %s...\n", strings.Title(action), id, version)
		if err := c.Delete(id, version); err != nil {
			return err
		}
		fmt.Printf("Package %s.\n", action+"ed")
		return nil
	},
}

func init() {
	packageSearchCmd.Flags().Int("skip", 0, "number of results to skip")
	packageSearchCmd.Flags().Int("take", 20, "number of results to return")
	packageSearchCmd.Flags().Bool("prerelease", false, "include pre-release packages")
	packageSearchCmd.RegisterFlagCompletionFunc("properties", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return searchProperties, cobra.ShellCompDirectiveNoFileComp
	})

	packageListCmd.Flags().String("id", "", "package ID")
	packageListCmd.RegisterFlagCompletionFunc("properties", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return listProperties, cobra.ShellCompDirectiveNoFileComp
	})

	packageInfoCmd.Flags().String("version", "", "specific version (default: latest)")
	packageInfoCmd.RegisterFlagCompletionFunc("properties", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return infoProperties, cobra.ShellCompDirectiveNoFileComp
	})

	packagePullCmd.Flags().String("version", "", "version to download")
	packagePullCmd.Flags().String("output-dir", ".", "directory to save the package")
	packagePullCmd.MarkFlagRequired("version")

	packageUnlistCmd.Flags().String("version", "", "version to unlist/delete")
	packageUnlistCmd.Flags().Bool("force", false, "skip confirmation prompt")
	packageUnlistCmd.Flags().Bool("hard", false, "signal hard-delete intent (server must be configured for HardDelete)")
	packageUnlistCmd.MarkFlagRequired("version")

	packageCmd.AddCommand(packageSearchCmd)
	packageCmd.AddCommand(packageListCmd)
	packageCmd.AddCommand(packageInfoCmd)
	packageCmd.AddCommand(packagePushCmd)
	packageCmd.AddCommand(packagePullCmd)
	packageCmd.AddCommand(packageUnlistCmd)
}
