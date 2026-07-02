package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mwtrigg/nugctl/internal/client"
	"github.com/mwtrigg/nugctl/internal/config"
	"github.com/mwtrigg/nugctl/internal/output"
	"github.com/spf13/cobra"
)

// Environment variables slot in between CLI flags and the config file:
// CLI flag > env var > profile (config file) > default.
const (
	envProfile  = "NUGCTL_PROFILE"
	envURL      = "NUGCTL_URL"
	envAPIKey   = "NUGCTL_API_KEY"
	envInsecure = "NUGCTL_INSECURE"
)

var (
	flagProfile       string
	flagURL           string
	flagAPIKey        string
	flagOutput        string
	flagVerbose       bool
	flagInsecure      bool
	flagAllProperties bool
	flagProperties    []string
)

var rootCmd = &cobra.Command{
	Use:   "nugctl",
	Short: "CLI for BaGetter and NuGet package feeds",
	Long:  "nugctl manages packages on BaGetter and NuGet-compatible feeds.",
	Example: `  nugctl auth login --name prod --url https://nuget.example.com/v3/index.json
  nugctl package search newtonsoft --take 50
  nugctl package push MyLib.1.2.3.nupkg
  nugctl feed info --insecure   # self-signed feed`,
}

func Execute(version string) {
	rootCmd.Version = version
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagProfile, "profile", "p", "", "profile to use (env: "+envProfile+")")
	rootCmd.PersistentFlags().StringVar(&flagURL, "url", "", "override feed URL (env: "+envURL+")")
	rootCmd.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "override API key (env: "+envAPIKey+")")
	rootCmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", "table", "output format: table|json|yaml")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "verbose HTTP logging")
	rootCmd.PersistentFlags().BoolVar(&flagInsecure, "insecure", false, "skip TLS certificate verification, accept self-signed certs (env: "+envInsecure+")")
	rootCmd.PersistentFlags().BoolVarP(&flagAllProperties, "all-properties", "A", false, "include all available properties in output")
	rootCmd.PersistentFlags().StringSliceVar(&flagProperties, "properties", nil, "specific properties to include (comma-separated or repeated)")

	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(feedCmd)
	rootCmd.AddCommand(packageCmd)
	rootCmd.AddCommand(profileCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(completionCmd)
}

// resolveClient loads config and returns a ready NuGet client.
// For each setting, precedence is: CLI flag > env var > profile (config file) > default.
func resolveClient() (*client.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	profileName := flagProfile
	if profileName == "" {
		profileName = os.Getenv(envProfile)
	}
	prof, err := cfg.ActiveProfile(profileName)
	if err != nil {
		return nil, err
	}

	u := prof.URL
	if v := os.Getenv(envURL); v != "" {
		u = v
	}
	if flagURL != "" {
		u = flagURL
	}
	if u == "" {
		return nil, fmt.Errorf("no URL configured for profile %q", prof.Name)
	}

	k := prof.APIKey
	if v := os.Getenv(envAPIKey); v != "" {
		k = v
	}
	if flagAPIKey != "" {
		k = flagAPIKey
	}

	insecure := prof.Insecure
	if v := os.Getenv(envInsecure); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			insecure = b
		}
	}
	if rootCmd.PersistentFlags().Lookup("insecure").Changed {
		insecure = flagInsecure
	}

	return client.New(u, k, flagVerbose, insecure), nil
}

// hasProperty returns true if prop is in --properties or --all-properties is set.
func hasProperty(prop string) bool {
	if flagAllProperties {
		return true
	}
	prop = strings.ToLower(prop)
	for _, p := range flagProperties {
		if strings.ToLower(p) == prop {
			return true
		}
	}
	return false
}

// effectiveFormat returns the output format to use, applying defaulting rules:
//   - explicit --output flag always wins
//   - --all-properties defaults to yaml
//   - --properties (named) defaults to table
//   - bare invocation defaults to table
func effectiveFormat() output.Format {
	if cmd := rootCmd.PersistentFlags().Lookup("output"); cmd != nil && cmd.Changed {
		f, _ := output.ParseFormat(flagOutput)
		return f
	}
	if flagAllProperties {
		return output.FormatYAML
	}
	f, _ := output.ParseFormat(flagOutput)
	return f
}

func die(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
