package cmd

import (
	"fmt"

	"github.com/mwtrigg/nugctl/internal/output"
	"github.com/spf13/cobra"
)

var feedCmd = &cobra.Command{
	Use:   "feed",
	Short: "Feed information",
}

var feedInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show feed service index and capabilities",
	Example: `  nugctl feed info
  nugctl feed info --all-properties -o yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := resolveClient()
		if err != nil {
			return err
		}
		idx, err := c.ServiceIndex()
		if err != nil {
			return err
		}

		switch effectiveFormat() {
		case output.FormatJSON:
			return output.PrintJSON(idx)
		case output.FormatYAML:
			return output.PrintYAML(idx)
		default:
			output.PrintKV([][2]string{
				{"URL", c.BaseURL},
				{"Version", idx.Version},
				{"Resources", ""},
			})
			println()

			if flagAllProperties || len(flagProperties) > 0 {
				headers := []string{"TYPE", "COMMENT", "URL"}
				rows := make([][]string, 0, len(idx.Resources))
				for _, r := range idx.Resources {
					rows = append(rows, []string{r.Type, r.Comment, r.ID})
				}
				output.PrintTable(headers, rows)
			} else {
				headers := []string{"TYPE", "COMMENT"}
				rows := make([][]string, 0, len(idx.Resources))
				for _, r := range idx.Resources {
					rows = append(rows, []string{r.Type, r.Comment})
				}
				output.PrintTable(headers, rows)
			}
		}
		return nil
	},
}

// feedProperties are valid --properties values for feed commands.
var feedProperties = []string{"url"}

func init() {
	feedInfoCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	feedInfoCmd.RegisterFlagCompletionFunc("properties", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return feedProperties, cobra.ShellCompDirectiveNoFileComp
	})
	feedCmd.AddCommand(feedInfoCmd)
}

func println() {
	fmt.Println()
}
