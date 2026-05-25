package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Account struct {
	Label    string    `json:"label"`
	Email    string    `json:"email"`
	LastUsed time.Time `json:"last_used"`
}

type Config struct {
	Accounts     []Account `json:"accounts"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
}

type RemoteCreds struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// Default remote URL to retrieve credentials if not overridden by AGY_MANAGER_CREDS_URL
const DefaultCredsURL = "https://raw.githubusercontent.com/supSugam/agy-manager-secrets/main/credentials.json"

func fetchFromURL(credsURL string, cli string) (string, string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(credsURL)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("failed to fetch credentials: status %d", resp.StatusCode)
	}

	var data map[string]RemoteCreds
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", "", err
	}

	creds, ok := data[cli]
	if !ok {
		return "", "", fmt.Errorf("no credentials found for CLI %s in remote file", cli)
	}

	return creds.ClientID, creds.ClientSecret, nil
}

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

	// 3. Dynamically retrieve credentials
	fmt.Printf("\n[agy-manager] Retrieving Google OAuth Client ID & Secret for '%s'...\n", cli)

	// Try fetching from remote URL
	credsURL := os.Getenv("AGY_MANAGER_CREDS_URL")
	if credsURL == "" {
		credsURL = DefaultCredsURL
	}

	fetchedID, fetchedSecret, err := fetchFromURL(credsURL, cli)
	if err == nil && fetchedID != "" && fetchedSecret != "" {
		id = fetchedID
		secret = fetchedSecret
		fmt.Printf("[agy-manager] Successfully retrieved credentials from remote source.\n")
	} else {
		if err != nil {
			fmt.Printf("[agy-manager] Remote retrieval failed: %v\n", err)
		}
		fmt.Println("Please manually enter the Google OAuth credentials for this CLI tool.")
		fmt.Println("These will be saved securely to your local configuration on this machine.")

		reader := bufio.NewReader(os.Stdin)

		fmt.Printf("Enter Google OAuth Client ID for '%s': ", cli)
		inputID, readErr := reader.ReadString('\n')
		if readErr == nil {
			id = strings.TrimSpace(inputID)
		}

		fmt.Printf("Enter Google OAuth Client Secret for '%s': ", cli)
		inputSecret, readErr := reader.ReadString('\n')
		if readErr == nil {
			secret = strings.TrimSpace(inputSecret)
		}
	}

	// 4. Save to config if successfully retrieved
	if id != "" && secret != "" {
		c.ClientID = id
		c.ClientSecret = secret
		err := SaveConfigForCLI(cli, c)
		if err != nil {
			fmt.Printf("[agy-manager] Warning: failed to save credentials to local config: %v\n", err)
		} else {
			fmt.Printf("[agy-manager] Saved credentials to local config: %s\n", cli)
		}
		return id, secret
	}

	return "", ""
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
