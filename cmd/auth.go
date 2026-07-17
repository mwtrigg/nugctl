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
  nugctl auth login --name self-signed --url https://feed.internal/v3/index.json --insecure
  nugctl auth login --name proxied --url https://feed.internal/v3/index.json --basic-auth-user svc-nuget`,
	RunE: func(cmd *cobra.Command, args []string) error {
		profileName, _ := cmd.Flags().GetString("name")
		u, _ := cmd.Flags().GetString("url")
		k, _ := cmd.Flags().GetString("api-key")
		insecure, _ := cmd.Flags().GetBool("insecure")
		basicUser, _ := cmd.Flags().GetString("basic-auth-user")
		basicPass, _ := cmd.Flags().GetString("basic-auth-pass")

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
		if basicUser == "" {
			fmt.Print("HTTP Basic Auth username (leave blank if not needed): ")
			basicUser, _ = reader.ReadString('\n')
			basicUser = strings.TrimSpace(basicUser)
		}
		if basicUser != "" && basicPass == "" {
			fmt.Print("HTTP Basic Auth password: ")
			b, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Println()
			if err != nil {
				basicPass, _ = reader.ReadString('\n')
				basicPass = strings.TrimSpace(basicPass)
			} else {
				basicPass = string(b)
			}
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}
		cfg.SetProfile(config.Profile{
			Name:          profileName,
			URL:           u,
			APIKey:        k,
			Insecure:      insecure,
			BasicAuthUser: basicUser,
			BasicAuthPass: basicPass,
		})
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
	Short: "Remove stored credentials (API key, Basic Auth) from a profile",
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
		prof.BasicAuthUser = ""
		prof.BasicAuthPass = ""
		cfg.SetProfile(*prof)
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Printf("Credentials removed from profile %q\n", name)
		return nil
	},
}

func init() {
	authLoginCmd.Flags().String("name", "", "profile name")
	authLoginCmd.Flags().String("url", "", "feed URL")
	authLoginCmd.Flags().String("api-key", "", "API key")
	authLoginCmd.Flags().Bool("insecure", false, "skip TLS certificate verification for this profile (accept self-signed certs)")
	authLoginCmd.Flags().String("basic-auth-user", "", "HTTP Basic Auth username")
	authLoginCmd.Flags().String("basic-auth-pass", "", "HTTP Basic Auth password")

	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
}
