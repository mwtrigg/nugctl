package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/mwtrigg/nugctl/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage feed authentication",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Add or update a profile with feed credentials",
	Example: `  nugctl auth login
  nugctl auth login --name prod --url https://nuget.example.com/v3/index.json --api-key XXXX
  nugctl auth login --name self-signed --url https://feed.internal/v3/index.json --insecure`,
	RunE: func(cmd *cobra.Command, args []string) error {
		profileName, _ := cmd.Flags().GetString("name")
		u, _ := cmd.Flags().GetString("url")
		k, _ := cmd.Flags().GetString("api-key")
		insecure, _ := cmd.Flags().GetBool("insecure")

		reader := bufio.NewReader(os.Stdin)

		if profileName == "" {
			fmt.Print("Profile name [default]: ")
			profileName, _ = reader.ReadString('\n')
			profileName = strings.TrimSpace(profileName)
			if profileName == "" {
				profileName = "default"
			}
		}
		if u == "" {
			fmt.Print("Feed URL: ")
			u, _ = reader.ReadString('\n')
			u = strings.TrimSpace(u)
		}
		if k == "" {
			fmt.Print("API key (leave blank for anonymous): ")
			b, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Println()
			if err != nil {
				// fallback for non-TTY
				k, _ = reader.ReadString('\n')
				k = strings.TrimSpace(k)
			} else {
				k = string(b)
			}
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cfg.SetProfile(config.Profile{Name: profileName, URL: u, APIKey: k, Insecure: insecure})
		if cfg.CurrentProfile == "" {
			cfg.CurrentProfile = profileName
		}
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("Profile %q saved. Active profile: %s\n", profileName, cfg.CurrentProfile)
		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove API key from a profile",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		name := flagProfile
		if name == "" {
			name = cfg.CurrentProfile
		}
		prof, err := cfg.ActiveProfile(name)
		if err != nil {
			return err
		}
		prof.APIKey = ""
		cfg.SetProfile(*prof)
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("API key removed from profile %q\n", name)
		return nil
	},
}

func init() {
	authLoginCmd.Flags().String("name", "", "profile name")
	authLoginCmd.Flags().String("url", "", "feed URL")
	authLoginCmd.Flags().String("api-key", "", "API key")
	authLoginCmd.Flags().Bool("insecure", false, "skip TLS certificate verification for this profile (accept self-signed certs)")

	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
}
