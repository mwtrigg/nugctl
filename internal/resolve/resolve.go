// Package resolve implements the shared CLI flag > env var > profile (config
// file) > default precedence for building a *client.Client, used by both the
// cobra CLI (cmd/root.go) and the TUI (internal/app).
package resolve

import (
	"fmt"
	"os"
	"strconv"

	"github.com/mwtrigg/nugctl/internal/client"
	"github.com/mwtrigg/nugctl/internal/config"
)

const (
	EnvProfile  = "NUGCTL_PROFILE"
	EnvURL      = "NUGCTL_URL"
	EnvAPIKey   = "NUGCTL_API_KEY"
	EnvInsecure = "NUGCTL_INSECURE"
)

// Overrides carries explicit values that win over env vars and the profile
// file, mirroring nugctl's CLI flags. InsecureSet distinguishes "the insecure
// flag was explicitly passed" from "the flag was left untouched" (the same
// distinction cobra's Flag.Changed makes).
type Overrides struct {
	Profile     string
	URL         string
	APIKey      string
	Insecure    bool
	InsecureSet bool
	Verbose     bool
}

// Client loads the config file and returns a ready NuGet client, applying the
// precedence: explicit override > env var > profile (config file) > default.
func Client(ov Overrides) (*client.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	return ClientFromConfig(cfg, ov)
}

// ClientFromConfig is like Client but takes an already-loaded *config.Config,
// so callers that keep cfg in memory (like the TUI, across profile switches)
// can avoid redundant disk reads.
func ClientFromConfig(cfg *config.Config, ov Overrides) (*client.Client, error) {
	profileName := ov.Profile
	if profileName == "" {
		profileName = os.Getenv(EnvProfile)
	}
	prof, err := cfg.ActiveProfile(profileName)
	if err != nil {
		return nil, err
	}

	u := prof.URL
	if v := os.Getenv(EnvURL); v != "" {
		u = v
	}
	if ov.URL != "" {
		u = ov.URL
	}
	if u == "" {
		return nil, fmt.Errorf("no URL configured for profile %q", prof.Name)
	}

	k := prof.APIKey
	if v := os.Getenv(EnvAPIKey); v != "" {
		k = v
	}
	if ov.APIKey != "" {
		k = ov.APIKey
	}

	insecure := prof.Insecure
	if v := os.Getenv(EnvInsecure); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			insecure = b
		}
	}
	if ov.InsecureSet {
		insecure = ov.Insecure
	}

	return client.New(u, k, ov.Verbose, insecure), nil
}
