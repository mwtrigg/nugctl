package resolve

import (
	"testing"

	"github.com/mwtrigg/nugctl/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		CurrentProfile: "default",
		Profiles: []config.Profile{
			{Name: "default", URL: "https://profile.example/v3/index.json", APIKey: "profile-key", Insecure: false},
		},
	}
}

func TestClientFromConfig_ProfileOnly(t *testing.T) {
	c, err := ClientFromConfig(testConfig(), Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BaseURL != "https://profile.example/v3/index.json" {
		t.Errorf("BaseURL = %q, want profile URL", c.BaseURL)
	}
	if c.APIKey != "profile-key" {
		t.Errorf("APIKey = %q, want profile key", c.APIKey)
	}
}

func TestClientFromConfig_EnvOverridesProfile(t *testing.T) {
	t.Setenv(EnvURL, "https://env.example/v3/index.json")
	t.Setenv(EnvAPIKey, "env-key")
	t.Setenv(EnvInsecure, "true")

	c, err := ClientFromConfig(testConfig(), Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BaseURL != "https://env.example/v3/index.json" {
		t.Errorf("BaseURL = %q, want env URL", c.BaseURL)
	}
	if c.APIKey != "env-key" {
		t.Errorf("APIKey = %q, want env key", c.APIKey)
	}
}

func TestClientFromConfig_FlagOverridesEnv(t *testing.T) {
	t.Setenv(EnvURL, "https://env.example/v3/index.json")
	t.Setenv(EnvAPIKey, "env-key")

	c, err := ClientFromConfig(testConfig(), Overrides{
		URL:    "https://flag.example/v3/index.json",
		APIKey: "flag-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BaseURL != "https://flag.example/v3/index.json" {
		t.Errorf("BaseURL = %q, want flag URL", c.BaseURL)
	}
	if c.APIKey != "flag-key" {
		t.Errorf("APIKey = %q, want flag key", c.APIKey)
	}
}

func TestClientFromConfig_InsecurePrecedence(t *testing.T) {
	// profile default false, env true, flag explicitly false -> flag wins.
	t.Setenv(EnvInsecure, "true")

	c, err := ClientFromConfig(testConfig(), Overrides{Insecure: false, InsecureSet: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Insecure {
		t.Error("Insecure = true, want false (explicit flag should override env)")
	}

	// env true, no flag -> env wins over profile's false.
	c2, err := ClientFromConfig(testConfig(), Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c2.Insecure {
		t.Error("Insecure = false, want true (env should override profile default)")
	}
}

func TestClientFromConfig_ProfileNameOverride(t *testing.T) {
	cfg := &config.Config{
		CurrentProfile: "a",
		Profiles: []config.Profile{
			{Name: "a", URL: "https://a.example/v3/index.json"},
			{Name: "b", URL: "https://b.example/v3/index.json"},
		},
	}
	c, err := ClientFromConfig(cfg, Overrides{Profile: "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BaseURL != "https://b.example/v3/index.json" {
		t.Errorf("BaseURL = %q, want profile b's URL", c.BaseURL)
	}
}

func TestClientFromConfig_EnvProfileOverride(t *testing.T) {
	t.Setenv(EnvProfile, "b")
	cfg := &config.Config{
		CurrentProfile: "a",
		Profiles: []config.Profile{
			{Name: "a", URL: "https://a.example/v3/index.json"},
			{Name: "b", URL: "https://b.example/v3/index.json"},
		},
	}
	c, err := ClientFromConfig(cfg, Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BaseURL != "https://b.example/v3/index.json" {
		t.Errorf("BaseURL = %q, want env-selected profile b's URL", c.BaseURL)
	}
}

func TestClientFromConfig_BasicAuthFromProfile(t *testing.T) {
	cfg := &config.Config{
		CurrentProfile: "default",
		Profiles: []config.Profile{
			{Name: "default", URL: "https://profile.example/v3/index.json", BasicAuthUser: "profile-user", BasicAuthPass: "profile-pass"},
		},
	}
	c, err := ClientFromConfig(cfg, Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BasicAuthUser != "profile-user" || c.BasicAuthPass != "profile-pass" {
		t.Errorf("BasicAuth = %q/%q, want profile-user/profile-pass", c.BasicAuthUser, c.BasicAuthPass)
	}
}

func TestClientFromConfig_BasicAuthEnvOverridesProfile(t *testing.T) {
	cfg := &config.Config{
		CurrentProfile: "default",
		Profiles: []config.Profile{
			{Name: "default", URL: "https://profile.example/v3/index.json", BasicAuthUser: "profile-user", BasicAuthPass: "profile-pass"},
		},
	}
	t.Setenv(EnvBasicAuthUser, "env-user")
	t.Setenv(EnvBasicAuthPass, "env-pass")

	c, err := ClientFromConfig(cfg, Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BasicAuthUser != "env-user" || c.BasicAuthPass != "env-pass" {
		t.Errorf("BasicAuth = %q/%q, want env-user/env-pass", c.BasicAuthUser, c.BasicAuthPass)
	}
}

func TestClientFromConfig_BasicAuthFlagOverridesEnv(t *testing.T) {
	cfg := &config.Config{
		CurrentProfile: "default",
		Profiles: []config.Profile{
			{Name: "default", URL: "https://profile.example/v3/index.json", BasicAuthUser: "profile-user", BasicAuthPass: "profile-pass"},
		},
	}
	t.Setenv(EnvBasicAuthUser, "env-user")
	t.Setenv(EnvBasicAuthPass, "env-pass")

	c, err := ClientFromConfig(cfg, Overrides{BasicAuthUser: "flag-user", BasicAuthPass: "flag-pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BasicAuthUser != "flag-user" || c.BasicAuthPass != "flag-pass" {
		t.Errorf("BasicAuth = %q/%q, want flag-user/flag-pass", c.BasicAuthUser, c.BasicAuthPass)
	}
}

func TestClientFromConfig_NoURL(t *testing.T) {
	cfg := &config.Config{
		CurrentProfile: "empty",
		Profiles:       []config.Profile{{Name: "empty"}},
	}
	if _, err := ClientFromConfig(cfg, Overrides{}); err == nil {
		t.Fatal("expected error for profile with no URL")
	}
}

func TestClientFromConfig_NoProfileConfigured_URLFlagIsEnough(t *testing.T) {
	c, err := ClientFromConfig(&config.Config{}, Overrides{
		URL:    "https://flag.example/v3/index.json",
		APIKey: "flag-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BaseURL != "https://flag.example/v3/index.json" {
		t.Errorf("BaseURL = %q, want flag URL", c.BaseURL)
	}
	if c.APIKey != "flag-key" {
		t.Errorf("APIKey = %q, want flag key", c.APIKey)
	}
}

func TestClientFromConfig_NoProfileConfigured_URLEnvIsEnough(t *testing.T) {
	t.Setenv(EnvURL, "https://env.example/v3/index.json")
	c, err := ClientFromConfig(&config.Config{}, Overrides{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.BaseURL != "https://env.example/v3/index.json" {
		t.Errorf("BaseURL = %q, want env URL", c.BaseURL)
	}
}

func TestClientFromConfig_NoProfileConfiguredAndNoURL_StillErrors(t *testing.T) {
	if _, err := ClientFromConfig(&config.Config{}, Overrides{}); err == nil {
		t.Fatal("expected error when neither a profile nor a URL is available")
	}
}

func TestClientFromConfig_NamedProfileMissing_ErrorsEvenWithURLFlag(t *testing.T) {
	// An explicitly-requested profile that doesn't exist should still be a
	// hard error (likely a typo) even if --url could otherwise stand in.
	_, err := ClientFromConfig(&config.Config{}, Overrides{
		Profile: "does-not-exist",
		URL:     "https://flag.example/v3/index.json",
	})
	if err == nil {
		t.Fatal("expected error for a named profile that doesn't exist")
	}
}
