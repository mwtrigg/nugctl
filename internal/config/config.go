package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Profile struct {
	Name          string `yaml:"name"`
	URL           string `yaml:"url"`
	APIKey        string `yaml:"api_key,omitempty"`
	Insecure      bool   `yaml:"insecure,omitempty"`
	BasicAuthUser string `yaml:"basic_auth_user,omitempty"`
	BasicAuthPass string `yaml:"basic_auth_pass,omitempty"`
}

type Config struct {
	CurrentProfile string    `yaml:"current_profile"`
	Profiles       []Profile `yaml:"profiles"`
}

func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "nugctl", "config.yml"), nil
}

func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func Save(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (c *Config) ActiveProfile(override string) (*Profile, error) {
	name := c.CurrentProfile
	if override != "" {
		name = override
	}
	for i := range c.Profiles {
		if c.Profiles[i].Name == name {
			return &c.Profiles[i], nil
		}
	}
	if name == "" {
		return nil, fmt.Errorf("no profile configured — run: nugctl auth login")
	}
	return nil, fmt.Errorf("profile %q not found", name)
}

func (c *Config) SetProfile(p Profile) {
	for i := range c.Profiles {
		if c.Profiles[i].Name == p.Name {
			c.Profiles[i] = p
			return
		}
	}
	c.Profiles = append(c.Profiles, p)
}

func (c *Config) DeleteProfile(name string) bool {
	for i, p := range c.Profiles {
		if p.Name == name {
			c.Profiles = append(c.Profiles[:i], c.Profiles[i+1:]...)
			if c.CurrentProfile == name {
				c.CurrentProfile = ""
			}
			return true
		}
	}
	return false
}
