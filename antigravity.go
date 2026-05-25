package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/base64"
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
	_ "modernc.org/sqlite"
)

const (
	AntigravityManagerSvc = "agy-manager-antigravity"
)

func antigravityDbPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "Antigravity", "User", "globalStorage", "state.vscdb")
}

// protobuf encoding/decoding helpers
func decodeVarint(b []byte) (uint64, int) {
	var v uint64
	var shift uint
	for i, c := range b {
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, i + 1
		}
		shift += 7
	}
	return 0, 0
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 1<<7 {
		b = append(b, byte(v&0x7f|0x80))
		v >>= 7
	}
	return append(b, byte(v))
}

func appendString(b []byte, tag uint32, s string) []byte {
	b = appendVarint(b, uint64(tag)<<3|2)
	b = appendVarint(b, uint64(len(s)))
	b = append(b, []byte(s)...)
	return b
}

func appendBytes(b []byte, tag uint32, d []byte) []byte {
	b = appendVarint(b, uint64(tag)<<3|2)
	b = appendVarint(b, uint64(len(d)))
	b = append(b, d...)
	return b
}

type TokenInfo struct {
	AccessToken  string
	RefreshToken string
	ExpiryMs     int64
	Email        string
}

func (t *TokenInfo) IsExpired() bool {
	return time.UnixMilli(t.ExpiryMs).Before(time.Now().Add(5 * time.Minute))
}

// decode token from inner protobuf (oauthTokenInfoSentinelKey payload)
func decodeInnerToken(data []byte) *TokenInfo {
	var token TokenInfo
	i := 0
	for i < len(data) {
		tW, n := decodeVarint(data[i:])
		i += n
		tag := tW >> 3
		wire := tW & 7

		if wire == 2 {
			l, n := decodeVarint(data[i:])
			i += n
			fData := data[i : i+int(l)]
			i += int(l)
			if tag == 1 {
				token.AccessToken = string(fData)
			} else if tag == 3 {
				token.RefreshToken = string(fData)
			} else if tag == 4 {
				// nested message containing expiry at tag 1
				j := 0
				for j < len(fData) {
					itW, n2 := decodeVarint(fData[j:])
					j += n2
					if itW>>3 == 1 && itW&7 == 0 {
						expSec, n3 := decodeVarint(fData[j:])
						j += n3
						token.ExpiryMs = int64(expSec) * 1000
					} else {
						// skip other fields
						break
					}
				}
			}
		} else {
			// not fully implemented skip, but we only expect length-delimited here mostly
			break
		}
	}
	return &token
}

func encodeInnerToken(token *TokenInfo) []byte {
	var b []byte
	b = appendString(b, 1, token.AccessToken)
	b = appendString(b, 2, "Bearer")
	b = appendString(b, 3, token.RefreshToken)

	var expMsg []byte
	expMsg = appendVarint(expMsg, uint64(1)<<3|0)
	expMsg = appendVarint(expMsg, uint64(token.ExpiryMs/1000))
	b = appendBytes(b, 4, expMsg)
	return b
}

func parseAntigravityOauthMap(encodedBase64 string) (map[string][]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encodedBase64)
	if err != nil {
		return nil, err
	}
	res := make(map[string][]byte)
	i := 0
	for i < len(data) {
		tW, n := decodeVarint(data[i:])
		if n == 0 {
			break
		}
		i += n
		if tW>>3 != 1 {
			break
		}
		entryLen, n := decodeVarint(data[i:])
		i += n
		entryData := data[i : i+int(entryLen)]
		i += int(entryLen)

		var key string
		var val []byte
		j := 0
		for j < len(entryData) {
			etW, n := decodeVarint(entryData[j:])
			j += n
			fLen, n := decodeVarint(entryData[j:])
			j += n
			fData := entryData[j : j+int(fLen)]
			j += int(fLen)
			if etW>>3 == 1 {
				key = string(fData)
			} else if etW>>3 == 2 {
				val = fData
			}
		}
		res[key] = val
	}
	return res, nil
}

func encodeAntigravityOauthMap(m map[string][]byte) string {
	var b []byte
	// keep stable order (oauthTokenInfoSentinelKey second usually)
	keys := []string{"authStateWithContextSentinelKey", "oauthTokenInfoSentinelKey"}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			var entry []byte
			entry = appendString(entry, 1, k)
			entry = appendBytes(entry, 2, v)
			b = appendBytes(b, 1, entry)
		}
	}
	return base64.StdEncoding.EncodeToString(b)
}

func getActiveAntigravityToken() (*TokenInfo, error) {
	db, err := sql.Open("sqlite", antigravityDbPath())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var oauthStr, authStatusStr string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'antigravityUnifiedStateSync.oauthToken'").Scan(&oauthStr)
	if err != nil {
		return nil, err
	}
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'antigravityAuthStatus'").Scan(&authStatusStr)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	m, err := parseAntigravityOauthMap(oauthStr)
	if err != nil {
		return nil, err
	}

	val, ok := m["oauthTokenInfoSentinelKey"]
	if !ok {
		return nil, fmt.Errorf("oauthTokenInfoSentinelKey not found")
	}

	// val is a wrapper message with tag 1
	var innerData []byte
	j := 0
	tW, n := decodeVarint(val[j:])
	j += n
	if tW>>3 == 1 {
		l, n := decodeVarint(val[j:])
		j += n
		innerData = val[j : j+int(l)]
	} else {
		return nil, fmt.Errorf("unexpected outer tag in val")
	}

	decodedInner, err := base64.StdEncoding.DecodeString(string(innerData))
	if err != nil {
		return nil, fmt.Errorf("failed to decode inner base64: %v", err)
	}

	token := decodeInnerToken(decodedInner)

	if authStatusStr != "" {
		var status map[string]interface{}
		if err := json.Unmarshal([]byte(authStatusStr), &status); err == nil {
			if em, ok := status["email"].(string); ok {
				token.Email = em
			}
		}
	}

	return token, nil
}

func saveActiveAntigravityToken(token *TokenInfo) error {
	db, err := sql.Open("sqlite", antigravityDbPath())
	if err != nil {
		return err
	}
	defer db.Close()

	var oauthStr string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'antigravityUnifiedStateSync.oauthToken'").Scan(&oauthStr)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	var m map[string][]byte
	if oauthStr != "" {
		m, err = parseAntigravityOauthMap(oauthStr)
		if err != nil {
			m = make(map[string][]byte)
		}
	} else {
		m = make(map[string][]byte)
	}

	innerData := encodeInnerToken(token)
	encodedInner := base64.StdEncoding.EncodeToString(innerData)
	var val []byte
	val = appendString(val, 1, encodedInner)
	m["oauthTokenInfoSentinelKey"] = val

	newOauthStr := encodeAntigravityOauthMap(m)

	_, err = db.Exec("INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)", "antigravityUnifiedStateSync.oauthToken", newOauthStr)
	if err != nil {
		return err
	}

	var authStatusStr string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'antigravityAuthStatus'").Scan(&authStatusStr)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	var status map[string]interface{}
	if authStatusStr != "" {
		json.Unmarshal([]byte(authStatusStr), &status)
	}
	if status == nil {
		status = make(map[string]interface{})
	}
	status["apiKey"] = token.AccessToken
	status["email"] = token.Email
	
	newStatusStr, _ := json.Marshal(status)
	_, err = db.Exec("INSERT OR REPLACE INTO ItemTable (key, value) VALUES (?, ?)", "antigravityAuthStatus", string(newStatusStr))
	return err
}

func deleteActiveAntigravityToken() error {
	db, err := sql.Open("sqlite", antigravityDbPath())
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("DELETE FROM ItemTable WHERE key IN ('antigravityUnifiedStateSync.oauthToken', 'antigravityAuthStatus')")
	return err
}

func getSavedAntigravityToken(email string) (*TokenInfo, error) {
	secret, err := keyring.Get(AntigravityManagerSvc, email)
	if err != nil {
		return nil, err
	}
	var token TokenInfo
	if err := json.Unmarshal([]byte(secret), &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func saveAntigravityTokenForAccount(email string, token *TokenInfo) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return keyring.Set(AntigravityManagerSvc, email, string(data))
}

func deleteSavedAntigravityToken(email string) error {
	return keyring.Delete(AntigravityManagerSvc, email)
}

func RefreshAntigravityToken(token *TokenInfo) error {
	if token.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	cfg, err := LoadConfigForCLI("antigravity")
	if err != nil {
		return err
	}
	clientID, clientSecret := cfg.GetCredentials("antigravity")

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
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse refresh response: %w", err)
	}
	token.AccessToken = result.AccessToken
	token.ExpiryMs = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second).UnixMilli()
	return nil
}

func FetchAntigravityEmail(token *TokenInfo) (string, error) {
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
		if rerr := RefreshAntigravityToken(token); rerr != nil {
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

func cmdAntigravityList() {
	cfg, err := LoadConfigForCLI("antigravity")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	activeToken, _ := getActiveAntigravityToken()
	activeRefreshToken := ""
	if activeToken != nil {
		activeRefreshToken = activeToken.RefreshToken
	}

	if len(cfg.Accounts) == 0 {
		fmt.Println("No accounts saved for antigravity.")
		fmt.Println("If you have an active session, run 'agy-manager --cli antigravity add' to save it.")
		if activeToken != nil {
			fmt.Println("\nCurrently active antigravity IDE token:")
			email, err := FetchAntigravityEmail(activeToken)
			if err == nil {
				fmt.Printf("  Email:  %s\n", email)
			} else if activeToken.Email != "" {
				fmt.Printf("  Email:  %s (cached, fetch failed: %v)\n", activeToken.Email, err)
			} else {
				fmt.Printf("  Email:  Unknown (fetch failed: %v)\n", err)
			}
			fmt.Printf("  Expiry: %s\n", time.UnixMilli(activeToken.ExpiryMs).Format(time.RFC822))
		}
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ACTIVE\tLABEL\tEMAIL\tLAST USED\tEXPIRY STATUS")
	for _, acc := range cfg.Accounts {
		isActive := ""
		token, err := getSavedAntigravityToken(acc.Email)
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
				timeLeft := time.Until(time.UnixMilli(token.ExpiryMs)).Round(time.Minute)
				expiryStr = fmt.Sprintf("Expires in %v", timeLeft)
			}
		} else if err != nil {
			expiryStr = fmt.Sprintf("Error reading: %v", err)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", isActive, acc.Label, acc.Email, FormatLastUsed(acc.LastUsed), expiryStr)
	}
	w.Flush()
}

func cmdAntigravityAdd(initialLabel string) {
	_, err := exec.LookPath("agy")
	if err != nil {
		fmt.Println("Error: 'agy' command not found in PATH. We need agy to authenticate for Antigravity IDE.")
		os.Exit(1)
	}

	cfg, err := LoadConfigForCLI("antigravity")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	activeToken, activeErr := getActiveAntigravityToken()
	var activeEmail string
	if activeErr == nil && activeToken != nil {
		fmt.Println("Retrieving current antigravity IDE session details...")
		if activeToken.Email != "" {
			activeEmail = activeToken.Email
		} else {
			email, err := FetchAntigravityEmail(activeToken)
			if err == nil {
				activeEmail = email
				activeToken.Email = email
			}
		}
		if activeEmail != "" {
			fmt.Printf("You are currently logged in as: %s\n", activeEmail)
			err = saveAntigravityTokenForAccount(activeEmail, activeToken)
			if err != nil {
				fmt.Printf("Warning: failed to save current session: %v\n", err)
			} else {
				label := strings.Split(activeEmail, "@")[0]
				cfg.AddOrUpdateAccount(label, activeEmail)
				_ = SaveConfigForCLI("antigravity", cfg)
				fmt.Printf("Current session auto-saved as label '%s'.\n", label)
			}
		} else {
			fmt.Println("Warning: Could not fetch email for current session.")
		}
	}

	confirm := promptString("To add a new account, please log out and log in with the new account using the Antigravity IDE UI.\nWhen you are successfully logged in, return here and press Enter to continue. [Enter to continue / q to quit]: ", "")
	if strings.ToLower(confirm) == "q" || strings.ToLower(confirm) == "quit" {
		fmt.Println("Aborted.")
		os.Exit(0)
	}

	fmt.Println("Reading new session from Antigravity IDE...")
	newToken, err := getActiveAntigravityToken()
	if err != nil || newToken == nil {
		fmt.Println("Error: No session found. Please make sure you are logged into Antigravity IDE.")
		os.Exit(1)
	}

	fmt.Println("Fetching details for the new account...")
	newEmail, err := FetchAntigravityEmail(newToken)
	if err != nil {
		fmt.Printf("Error verifying new token: %v\n", err)
		os.Exit(1)
	}
	newToken.Email = newEmail
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

	err = saveAntigravityTokenForAccount(newEmail, newToken)
	if err != nil {
		fmt.Printf("Error saving token: %v\n", err)
		os.Exit(1)
	}

	cfg.AddOrUpdateAccount(label, newEmail)
	_ = SaveConfigForCLI("antigravity", cfg)

	// Finally, inject this into Antigravity IDE's state.vscdb
	err = saveActiveAntigravityToken(newToken)
	if err != nil {
		fmt.Printf("Warning: Could not set as active session in Antigravity IDE: %v\n", err)
	}

	fmt.Printf("\nSaved antigravity IDE account '%s' (%s) successfully.\n", label, newEmail)
	fmt.Println("This account is now the active account.")
	fmt.Println("Please restart Antigravity IDE for changes to take effect.")
}

func cmdAntigravitySwitch(labelOrEmail string) {
	cfg, err := LoadConfigForCLI("antigravity")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	acc, found := cfg.GetAccount(labelOrEmail)
	if !found {
		fmt.Printf("Error: account matching '%s' not found.\n", labelOrEmail)
		fmt.Println("Available accounts:")
		cmdAntigravityList()
		os.Exit(1)
	}

	token, err := getSavedAntigravityToken(acc.Email)
	if err != nil {
		fmt.Printf("Error retrieving token for %s: %v\n", acc.Email, err)
		os.Exit(1)
	}

	if token.IsExpired() {
		fmt.Println("Token is expired or expiring soon. Refreshing...")
		err = RefreshAntigravityToken(token)
		if err != nil {
			fmt.Printf("Warning: Failed to refresh token: %v. Storing anyway.\n", err)
		} else {
			_ = saveAntigravityTokenForAccount(acc.Email, token)
		}
	}

	err = saveActiveAntigravityToken(token)
	if err != nil {
		fmt.Printf("Error writing active token to state.vscdb: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Switched active antigravity IDE account to: %s (%s)\n", acc.Email, acc.Label)
	fmt.Println("Please restart Antigravity IDE for changes to take effect.")
}

func cmdAntigravityRemove(labelOrEmail string) {
	cfg, err := LoadConfigForCLI("antigravity")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	acc, found := cfg.GetAccount(labelOrEmail)
	if !found {
		fmt.Printf("Error: account matching '%s' not found.\n", labelOrEmail)
		os.Exit(1)
	}

	confirm := promptString(fmt.Sprintf("Are you sure you want to remove antigravity account '%s' (%s)? [y/N]: ", acc.Label, acc.Email), "n")
	if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
		fmt.Println("Aborted.")
		os.Exit(0)
	}

	_ = deleteSavedAntigravityToken(acc.Email)

	activeToken, activeErr := getActiveAntigravityToken()
	if activeErr == nil && activeToken != nil {
		saved, savedErr := getSavedAntigravityToken(acc.Email)
		if savedErr == nil && saved != nil && saved.RefreshToken == activeToken.RefreshToken {
			fmt.Println("Logging out active session as it was deleted...")
			_ = deleteActiveAntigravityToken()
		}
	}

	cfg.RemoveAccount(acc.Email)
	_ = SaveConfigForCLI("antigravity", cfg)

	fmt.Printf("Successfully removed antigravity account '%s' (%s).\n", acc.Label, acc.Email)
}

func cmdAntigravityRename(oldLabel, newLabel string) {
	cfg, err := LoadConfigForCLI("antigravity")
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

	if err := SaveConfigForCLI("antigravity", cfg); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Renamed antigravity account '%s' to '%s' (%s).\n", oldLabel, newLabel, acc.Email)
}

func cmdAntigravityAccounts() {
	cfg, err := LoadConfigForCLI("antigravity")
	if err != nil {
		return
	}
	for _, acc := range cfg.Accounts {
		fmt.Println(acc.Email)
	}
}
