package cmd

import (
	"fmt"

	"github.com/mwtrigg/nugctl/internal/config"
	"github.com/mwtrigg/nugctl/internal/output"
	"github.com/spf13/cobra"
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage feed profiles",
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		switch effectiveFormat() {
		case output.FormatJSON:
			return output.PrintJSON(cfg.Profiles)
		case output.FormatYAML:
			return output.PrintYAML(cfg.Profiles)
		default:
			headers := []string{"NAME", "URL", "HAS KEY", "INSECURE", "ACTIVE"}
			rows := make([][]string, 0, len(cfg.Profiles))
			for _, p := range cfg.Profiles {
				hasKey := "no"
				if p.APIKey != "" {
					hasKey = "yes"
				}
				insecure := "no"
				if p.Insecure {
					insecure = "yes"
				}
				active := ""
				if p.Name == cfg.CurrentProfile {
					active = "*"
				}
				rows = append(rows, []string{p.Name, p.URL, hasKey, insecure, active})
			}
			output.PrintTable(headers, rows)
		}
		return nil
	},
}

var profileUseCmd = &cobra.Command{
	Use:     "use <name>",
	Short:   "Set the active profile",
	Example: `  nugctl profile use prod`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if _, err := cfg.ActiveProfile(name); err != nil {
			return err
		}
		cfg.CurrentProfile = name
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("Active profile set to %q\n", name)
		return nil
	},
}

var profileDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if !cfg.DeleteProfile(name) {
			return fmt.Errorf("profile %q not found", name)
		}
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("Profile %q deleted\n", name)
		return nil
	},
}

func init() {
	profileCmd.AddCommand(profileListCmd)
	profileCmd.AddCommand(profileUseCmd)
	profileCmd.AddCommand(profileDeleteCmd)
}
