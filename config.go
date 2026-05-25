package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Account struct {
	Label    string    `json:"label"`
	Email    string    `json:"email"`
	LastUsed time.Time `json:"last_used"`
}

type Config struct {
	Accounts               []Account `json:"accounts"`
	ClientID               string    `json:"client_id,omitempty"`
	ClientSecret           string    `json:"client_secret,omitempty"`
	DiscoveredClientID     string    `json:"discovered_client_id,omitempty"`
	DiscoveredClientSecret string    `json:"discovered_client_secret,omitempty"`
}

const (
	// Default credentials for 'agy'
	AgyDefaultClientID     = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	AgyDefaultClientSecret = "" // Discovered dynamically at runtime

	// Default credentials for 'gemini'
	GeminiDefaultClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	GeminiDefaultClientSecret = "" // Discovered dynamically at runtime

	// Default credentials for 'antigravity'
	AntigravityDefaultClientID     = "884354919052-36trc1jjb3tguiac32ov6cod268c5blh.apps.googleusercontent.com"
	AntigravityDefaultClientSecret = "" // Discovered dynamically at runtime
)

func (c *Config) GetCredentials(cli string) (string, string) {
	id := c.ClientID
	secret := c.ClientSecret

	// 1. Check Config overrides
	if id != "" && secret != "" {
		return id, secret
	}

	// 2. Check Environment Variables
	envPrefix := ""
	switch cli {
	case "agy":
		envPrefix = "AGY"
	case "gemini":
		envPrefix = "GEMINI"
	case "antigravity":
		envPrefix = "ANTIGRAVITY"
	}

	if envId := os.Getenv(envPrefix + "_CLIENT_ID"); envId != "" {
		id = envId
	}
	if envSecret := os.Getenv(envPrefix + "_CLIENT_SECRET"); envSecret != "" {
		secret = envSecret
	}

	if id != "" && secret != "" {
		return id, secret
	}

	// 3. Try Dynamic Discovery
	discID, discSecret := "", ""
	switch cli {
	case "agy":
		discID, discSecret = DiscoverAgyCredentials()
	case "gemini":
		discID, discSecret = DiscoverGeminiCredentials()
	case "antigravity":
		discID, discSecret = DiscoverAntigravityCredentials()
	}

	if discID != "" && discSecret != "" {
		// Update cache if changed
		if discID != c.DiscoveredClientID || discSecret != c.DiscoveredClientSecret {
			c.DiscoveredClientID = discID
			c.DiscoveredClientSecret = discSecret
			_ = SaveConfigForCLI(cli, c)
		}
		return discID, discSecret
	}

	// 4. Fallback to Cached Discovery
	if c.DiscoveredClientID != "" && c.DiscoveredClientSecret != "" {
		return c.DiscoveredClientID, c.DiscoveredClientSecret
	}

	// 5. Fallback to hardcoded defaults
	switch cli {
	case "agy":
		return AgyDefaultClientID, AgyDefaultClientSecret
	case "gemini":
		return GeminiDefaultClientID, GeminiDefaultClientSecret
	case "antigravity":
		return AntigravityDefaultClientID, AntigravityDefaultClientSecret
	default:
		return "", ""
	}
}

func getConfigPath() (string, error) {
	return getConfigPathForCLI("agy")
}

func getConfigPathForCLI(cli string) (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	// e.g. ~/.config/agy-manager/agy.json or ~/.config/agy-manager/gemini.json
	return filepath.Join(configDir, "agy-manager", cli+".json"), nil
}

func LoadConfig() (*Config, error) {
	return LoadConfigForCLI("agy")
}

func LoadConfigForCLI(cli string) (*Config, error) {
	path, err := getConfigPathForCLI(cli)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &Config{Accounts: []Account{}}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SaveConfig(cfg *Config) error {
	return SaveConfigForCLI("agy", cfg)
}

func SaveConfigForCLI(cli string, cfg *Config) error {
	path, err := getConfigPathForCLI(cli)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func (c *Config) GetAccount(labelOrEmail string) (Account, bool) {
	for _, acc := range c.Accounts {
		if acc.Label == labelOrEmail || acc.Email == labelOrEmail {
			return acc, true
		}
	}
	return Account{}, false
}

func (c *Config) AddOrUpdateAccount(label, email string) {
	for i, acc := range c.Accounts {
		if acc.Email == email {
			c.Accounts[i].Label = label
			c.Accounts[i].LastUsed = time.Now()
			return
		}
	}
	c.Accounts = append(c.Accounts, Account{Label: label, Email: email, LastUsed: time.Now()})
}

func (c *Config) RemoveAccount(labelOrEmail string) bool {
	for i, acc := range c.Accounts {
		if acc.Label == labelOrEmail || acc.Email == labelOrEmail {
			c.Accounts = append(c.Accounts[:i], c.Accounts[i+1:]...)
			return true
		}
	}
	return false
}

func FormatLastUsed(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%02ds ago", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%02dm ago", int(d.Minutes()))
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%02dh %02dm ago", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	return fmt.Sprintf("%dd ago", days)
}
