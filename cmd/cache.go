package cmd

import (
	"fmt"

	"github.com/mwtrigg/nugctl/internal/client"
	"github.com/spf13/cobra"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage the cached feed service index",
}

var cacheClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove all cached feed service indexes",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := client.ClearCache(); err != nil {
			return err
		}
		fmt.Println("Cache cleared.")
		return nil
	},
}

func init() {
	cacheCmd.AddCommand(cacheClearCmd)
}
