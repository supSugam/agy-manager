package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	GeminiDir        = ".gemini"
	GeminiOAuthFile  = "oauth_creds.json"
	GeminiManagerSvc = "agy-manager-gemini"
)

// GeminiToken mirrors the structure of ~/.gemini/oauth_creds.json
type GeminiToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiryDate   int64  `json:"expiry_date"` // Unix ms
}

func (t *GeminiToken) IsExpired() bool {
	// ExpiryDate is in milliseconds
	return time.UnixMilli(t.ExpiryDate).Before(time.Now().Add(5 * time.Minute))
}

func geminiOAuthFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, GeminiDir, GeminiOAuthFile)
}

// ── Active token (the live ~/.gemini/oauth_creds.json) ──────────────────────

func getActiveGeminiToken() (*GeminiToken, error) {
	path := geminiOAuthFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var token GeminiToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("failed to parse gemini oauth file: %w", err)
	}
	return &token, nil
}

func saveActiveGeminiToken(token *GeminiToken) error {
	path := geminiOAuthFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func deleteActiveGeminiToken() error {
	return os.Remove(geminiOAuthFilePath())
}

// ── Saved tokens (keyring under agy-manager-gemini / email) ─────────────────

func getSavedGeminiToken(email string) (*GeminiToken, error) {
	secret, err := keyring.Get(GeminiManagerSvc, email)
	if err != nil {
		return nil, err
	}
	var token GeminiToken
	if err := json.Unmarshal([]byte(secret), &token); err != nil {
		return nil, fmt.Errorf("failed to unmarshal saved gemini token: %w", err)
	}
	return &token, nil
}

func saveGeminiTokenForAccount(email string, token *GeminiToken) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return keyring.Set(GeminiManagerSvc, email, string(data))
}

func deleteSavedGeminiToken(email string) error {
	return keyring.Delete(GeminiManagerSvc, email)
}

// ── Token refresh ────────────────────────────────────────────────────────────

func RefreshGeminiToken(token *GeminiToken) error {
	if token.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	cfg, err := LoadConfigForCLI("gemini")
	if err != nil {
		return err
	}
	clientID, clientSecret := cfg.GetCredentials("gemini")

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"refresh_token": {token.RefreshToken},
		"grant_type":    {"refresh_token"},
	})
	if err != nil {
		return fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token refresh failed (%d): %s", resp.StatusCode, string(body))
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		TokenType   string `json:"token_type"`
		IDToken     string `json:"id_token,omitempty"`
		Scope       string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse refresh response: %w", err)
	}
	token.AccessToken = result.AccessToken
	token.ExpiryDate = time.Now().Add(time.Duration(result.ExpiresIn)*time.Second).UnixMilli()
	if result.TokenType != "" {
		token.TokenType = result.TokenType
	}
	if result.IDToken != "" {
		token.IDToken = result.IDToken
	}
	if result.Scope != "" {
		token.Scope = result.Scope
	}
	return nil
}

// ── Fetch email via userinfo endpoint ────────────────────────────────────────

func FetchGeminiEmail(token *GeminiToken) (string, error) {
	req, err := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && token.RefreshToken != "" {
		// Try refreshing then retry
		if rerr := RefreshGeminiToken(token); rerr != nil {
			return "", fmt.Errorf("token expired and refresh failed: %w", rerr)
		}
		req2, _ := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
		req2.Header.Set("Authorization", "Bearer "+token.AccessToken)
		resp2, err2 := client.Do(req2)
		if err2 != nil {
			return "", err2
		}
		defer resp2.Body.Close()
		resp = resp2
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("userinfo request failed (%d): %s", resp.StatusCode, string(body))
	}
	var result struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		return "", err
	}
	if result.Email == "" {
		return "", fmt.Errorf("no email in userinfo response")
	}
	return result.Email, nil
}

// ── Commands ─────────────────────────────────────────────────────────────────

func cmdGeminiList() {
	cfg, err := LoadConfigForCLI("gemini")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	activeToken, _ := getActiveGeminiToken()
	activeRefreshToken := ""
	if activeToken != nil {
		activeRefreshToken = activeToken.RefreshToken
	}

	if len(cfg.Accounts) == 0 {
		fmt.Println("No accounts saved for gemini-cli.")
		fmt.Println("If you have an active session, run 'agy-manager --cli gemini add' to save it.")
		if activeToken != nil {
			fmt.Println("\nCurrently active gemini-cli token:")
			email, err := FetchGeminiEmail(activeToken)
			if err == nil {
				fmt.Printf("  Email:  %s\n", email)
			}
			fmt.Printf("  Expiry: %s\n", time.UnixMilli(activeToken.ExpiryDate).Format(time.RFC822))
		}
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ACTIVE\tLABEL\tEMAIL\tLAST USED\tEXPIRY STATUS")
	for _, acc := range cfg.Accounts {
		isActive := ""
		token, err := getSavedGeminiToken(acc.Email)
		if err == nil && token != nil && activeRefreshToken != "" {
			if token.RefreshToken == activeRefreshToken {
				isActive = "*"
			}
		}
		expiryStr := "Unknown"
		if err == nil && token != nil {
			if token.IsExpired() {
				expiryStr = "Expired (will refresh)"
			} else {
				timeLeft := time.Until(time.UnixMilli(token.ExpiryDate)).Round(time.Minute)
				expiryStr = fmt.Sprintf("Expires in %v", timeLeft)
			}
		} else if err != nil {
			expiryStr = fmt.Sprintf("Error reading: %v", err)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", isActive, acc.Label, acc.Email, FormatLastUsed(acc.LastUsed), expiryStr)
	}
	w.Flush()
}

func cmdGeminiAdd(initialLabel string) {
	_, err := exec.LookPath("gemini")
	if err != nil {
		fmt.Println("Error: 'gemini' command not found in PATH.")
		fmt.Println("Please install gemini-cli first.")
		os.Exit(1)
	}

	cfg, err := LoadConfigForCLI("gemini")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Back up current active token
	activeToken, activeErr := getActiveGeminiToken()
	var activeEmail string
	if activeErr == nil && activeToken != nil {
		fmt.Println("Retrieving current gemini-cli session details...")
		email, err := FetchGeminiEmail(activeToken)
		if err == nil {
			activeEmail = email
			fmt.Printf("You are currently logged in as: %s\n", activeEmail)
			err = saveGeminiTokenForAccount(activeEmail, activeToken)
			if err != nil {
				fmt.Printf("Warning: failed to save current session: %v\n", err)
			} else {
				label := strings.Split(activeEmail, "@")[0]
				cfg.AddOrUpdateAccount(label, activeEmail)
				_ = SaveConfigForCLI("gemini", cfg)
				fmt.Printf("Current session auto-saved as label '%s'.\n", label)
			}
		} else {
			fmt.Printf("Warning: Could not fetch email for current session: %v\n", err)
		}
	}

	confirm := promptString("To add a new account, we will temporarily log you out of gemini-cli.\nContinue? [y/N]: ", "n")
	if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
		fmt.Println("Aborted.")
		os.Exit(0)
	}

	fmt.Println("Clearing current gemini-cli session...")
	_ = deleteActiveGeminiToken()

	fmt.Println("\nStarting gemini to authenticate the new account.")
	fmt.Println("Please log in through the browser when prompted.")
	fmt.Println("----------------------------------------------------------------------")

	cmdExec := exec.Command("gemini", "-p", "")
	cmdExec.Stdin = os.Stdin
	cmdExec.Stdout = os.Stdout
	cmdExec.Stderr = os.Stderr
	_ = cmdExec.Run()

	// gemini writes the token to oauth_creds.json upon auth
	fmt.Println("----------------------------------------------------------------------")

	newToken, err := getActiveGeminiToken()
	if err != nil || newToken == nil {
		fmt.Println("Error: No new gemini-cli session found after authentication.")
		if activeToken != nil {
			fmt.Println("Restoring previous session...")
			_ = saveActiveGeminiToken(activeToken)
		}
		os.Exit(1)
	}

	fmt.Println("Fetching details for the new account...")
	newEmail, err := FetchGeminiEmail(newToken)
	if err != nil {
		fmt.Printf("Error verifying new token: %v\n", err)
		if activeToken != nil {
			fmt.Println("Restoring previous session...")
			_ = saveActiveGeminiToken(activeToken)
		}
		os.Exit(1)
	}
	fmt.Printf("Successfully authenticated as: %s\n", newEmail)

	defaultLabel := initialLabel
	if defaultLabel == "" {
		defaultLabel = strings.Split(newEmail, "@")[0]
	}
	labelPrompt := fmt.Sprintf("Enter a label for this account (default: '%s'): ", defaultLabel)
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print(labelPrompt)
	label := defaultLabel
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			label = text
		}
	}

	err = saveGeminiTokenForAccount(newEmail, newToken)
	if err != nil {
		fmt.Printf("Error saving token: %v\n", err)
		if activeToken != nil {
			_ = saveActiveGeminiToken(activeToken)
		}
		os.Exit(1)
	}

	cfg.AddOrUpdateAccount(label, newEmail)
	_ = SaveConfigForCLI("gemini", cfg)

	fmt.Printf("\nSaved gemini-cli account '%s' (%s) successfully.\n", label, newEmail)
	fmt.Println("This account is now the active account.")
}

func cmdGeminiSwitch(labelOrEmail string) {
	cfg, err := LoadConfigForCLI("gemini")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	acc, found := cfg.GetAccount(labelOrEmail)
	if !found {
		fmt.Printf("Error: account matching '%s' not found.\n", labelOrEmail)
		fmt.Println("Available accounts:")
		cmdGeminiList()
		os.Exit(1)
	}

	token, err := getSavedGeminiToken(acc.Email)
	if err != nil {
		fmt.Printf("Error retrieving token for %s: %v\n", acc.Email, err)
		os.Exit(1)
	}

	if token.IsExpired() {
		fmt.Println("Token is expired or expiring soon. Refreshing...")
		err = RefreshGeminiToken(token)
		if err != nil {
			fmt.Printf("Warning: Failed to refresh token: %v. Storing anyway.\n", err)
		} else {
			_ = saveGeminiTokenForAccount(acc.Email, token)
		}
	}

	err = saveActiveGeminiToken(token)
	if err != nil {
		fmt.Printf("Error writing active token: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Switched active gemini-cli account to: %s (%s)\n", acc.Email, acc.Label)
}

func cmdGeminiRemove(labelOrEmail string) {
	cfg, err := LoadConfigForCLI("gemini")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	acc, found := cfg.GetAccount(labelOrEmail)
	if !found {
		fmt.Printf("Error: account matching '%s' not found.\n", labelOrEmail)
		os.Exit(1)
	}

	confirm := promptString(fmt.Sprintf("Are you sure you want to remove gemini-cli account '%s' (%s)? [y/N]: ", acc.Label, acc.Email), "n")
	if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
		fmt.Println("Aborted.")
		os.Exit(0)
	}

	_ = deleteSavedGeminiToken(acc.Email)

	// If this was the active account, clear it
	activeToken, activeErr := getActiveGeminiToken()
	if activeErr == nil && activeToken != nil {
		saved, savedErr := getSavedGeminiToken(acc.Email)
		if savedErr == nil && saved != nil && saved.RefreshToken == activeToken.RefreshToken {
			fmt.Println("Logging out active session as it was deleted...")
			_ = deleteActiveGeminiToken()
		}
	}

	cfg.RemoveAccount(acc.Email)
	_ = SaveConfigForCLI("gemini", cfg)

	fmt.Printf("Successfully removed gemini-cli account '%s' (%s).\n", acc.Label, acc.Email)
}

func cmdGeminiRename(oldLabel, newLabel string) {
	cfg, err := LoadConfigForCLI("gemini")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	acc, found := cfg.GetAccount(oldLabel)
	if !found {
		fmt.Printf("Error: account with label '%s' not found.\n", oldLabel)
		os.Exit(1)
	}

	if _, exists := cfg.GetAccount(newLabel); exists {
		fmt.Printf("Error: label '%s' is already in use.\n", newLabel)
		os.Exit(1)
	}

	for i, a := range cfg.Accounts {
		if a.Email == acc.Email {
			cfg.Accounts[i].Label = newLabel
			break
		}
	}

	if err := SaveConfigForCLI("gemini", cfg); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Renamed gemini-cli account '%s' to '%s' (%s).\n", oldLabel, newLabel, acc.Email)
}

func cmdGeminiAccounts() {
	cfg, err := LoadConfigForCLI("gemini")
	if err != nil {
		return
	}
	for _, acc := range cfg.Accounts {
		fmt.Println(acc.Email)
	}
}
