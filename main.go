package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	AgyService     = "gemini"
	AgyUsername    = "antigravity"
	ManagerService = "agy-manager"
)

func getActiveToken() (*AgyToken, error) {
	secret, err := keyring.Get(AgyService, AgyUsername)
	if err != nil {
		return nil, err
	}
	var token AgyToken
	if err := json.Unmarshal([]byte(secret), &token); err != nil {
		return nil, fmt.Errorf("failed to unmarshal active token: %w", err)
	}
	return &token, nil
}

func saveActiveToken(token *AgyToken) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return keyring.Set(AgyService, AgyUsername, string(data))
}

func deleteActiveToken() error {
	return keyring.Delete(AgyService, AgyUsername)
}

func getSavedToken(email string) (*AgyToken, error) {
	secret, err := keyring.Get(ManagerService, email)
	if err != nil {
		return nil, err
	}
	var token AgyToken
	if err := json.Unmarshal([]byte(secret), &token); err != nil {
		return nil, fmt.Errorf("failed to unmarshal saved token: %w", err)
	}
	return &token, nil
}

func saveTokenForAccount(email string, token *AgyToken) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return keyring.Set(ManagerService, email, string(data))
}

func deleteSavedToken(email string) error {
	return keyring.Delete(ManagerService, email)
}

func promptString(promptText string, defaultValue string) string {
	fmt.Print(promptText)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			return defaultValue
		}
		return text
	}
	return defaultValue
}

func printUsage() {
	fmt.Println("Usage: agy-manager [--cli <name>] <command> [arguments]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --cli <name>   Target a specific CLI tool (agy, gemini, antigravity) [default: agy]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  ls                        List all saved accounts and show the active one")
	fmt.Println("  add [label]               Add a new account via interactive login")
	fmt.Println("  use <label_or_email>      Switch the active account to the specified one")
	fmt.Println("  rm <label_or_email>       Remove a saved account")
	fmt.Println("  rename <old> <new>        Rename an account label")
	fmt.Println("  help                      Show this help message")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  agy-manager ls")
	fmt.Println("  agy-manager add personal")
	fmt.Println("  agy-manager use work")
	fmt.Println("  agy-manager --cli gemini ls")
	fmt.Println("  agy-manager --cli antigravity use personal")
	fmt.Println("\nShell completion:")
	fmt.Println("  bash: source <(agy-manager completion bash)")
	fmt.Println("  zsh:  source <(agy-manager completion zsh)")
}

func main() {
	args := os.Args[1:]

	targetCLI := "agy"
	if len(args) >= 2 && args[0] == "--cli" {
		targetCLI = args[1]
		args = args[2:]
	}

	if len(args) == 0 {
		printUsage()
		os.Exit(0)
	}

	cmd := args[0]

	if targetCLI != "agy" && targetCLI != "gemini" && targetCLI != "antigravity" {
		fmt.Printf("Error: unknown CLI target '%s'. Supported: agy, gemini, antigravity\n", targetCLI)
		os.Exit(1)
	}

	switch cmd {
	case "ls":
		if targetCLI == "gemini" {
			cmdGeminiList()
		} else if targetCLI == "antigravity" {
			cmdAntigravityList()
		} else {
			cmdList()
		}
	case "add":
		label := ""
		if len(args) > 1 {
			label = args[1]
		}
		if targetCLI == "gemini" {
			cmdGeminiAdd(label)
		} else if targetCLI == "antigravity" {
			cmdAntigravityAdd(label)
		} else {
			cmdAdd(label)
		}
	case "use":
		if len(args) < 2 {
			fmt.Println("Error: 'use' requires a label or email")
			os.Exit(1)
		}
		if targetCLI == "gemini" {
			cmdGeminiSwitch(args[1])
		} else if targetCLI == "antigravity" {
			cmdAntigravitySwitch(args[1])
		} else {
			cmdSwitch(args[1])
		}
	case "rm":
		if len(args) < 2 {
			fmt.Println("Error: 'rm' requires a label or email")
			os.Exit(1)
		}
		if targetCLI == "gemini" {
			cmdGeminiRemove(args[1])
		} else if targetCLI == "antigravity" {
			cmdAntigravityRemove(args[1])
		} else {
			cmdRemove(args[1])
		}
	case "rename":
		if len(args) < 3 {
			fmt.Println("Error: 'rename' requires <old_label> and <new_label>")
			os.Exit(1)
		}
		if targetCLI == "gemini" {
			cmdGeminiRename(args[1], args[2])
		} else if targetCLI == "antigravity" {
			cmdAntigravityRename(args[1], args[2])
		} else {
			cmdRename(args[1], args[2])
		}
	case "_accounts":
		if targetCLI == "gemini" {
			cmdGeminiAccounts()
		} else if targetCLI == "antigravity" {
			cmdAntigravityAccounts()
		} else {
			cmdAccounts()
		}
	case "completion":
		shell := "bash"
		if len(args) >= 2 {
			shell = args[1]
		}
		cmdCompletion(shell)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func cmdList() {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	activeToken, err := getActiveToken()
	activeRefreshToken := ""
	if err == nil && activeToken != nil {
		activeRefreshToken = activeToken.Token.RefreshToken
	}

	if len(cfg.Accounts) == 0 {
		fmt.Println("No accounts saved in agy-manager.")
		fmt.Println("If you have an active session, run 'agy-manager add' to save it.")
		if activeToken != nil {
			fmt.Println("\nCurrently active token details:")
			email, err := FetchEmail(activeToken)
			if err == nil {
				fmt.Printf("  Email:  %s\n", email)
			}
			fmt.Printf("  Expiry: %s\n", activeToken.Token.Expiry.Format(time.RFC822))
		}
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "ACTIVE\tLABEL\tEMAIL\tLAST USED\tEXPIRY STATUS")

	for _, acc := range cfg.Accounts {
		isActive := ""
		token, err := getSavedToken(acc.Email)
		
		if err == nil && token != nil && activeRefreshToken != "" {
			if token.Token.RefreshToken == activeRefreshToken {
				isActive = "*"
			}
		}

		expiryStr := "Unknown"
		if err == nil && token != nil {
			if token.Token.Expiry.Before(time.Now()) {
				expiryStr = "Expired (will refresh)"
			} else {
				timeLeft := time.Until(token.Token.Expiry).Round(time.Minute)
				expiryStr = fmt.Sprintf("Expires in %v", timeLeft)
			}
		} else if err != nil {
			expiryStr = fmt.Sprintf("Error reading: %v", err)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", isActive, acc.Label, acc.Email, FormatLastUsed(acc.LastUsed), expiryStr)
	}
	w.Flush()
}

func cmdAdd(initialLabel string) {
	_, err := exec.LookPath("agy")
	if err != nil {
		fmt.Println("Error: 'agy' command line tool not found in PATH.")
		fmt.Println("Please install agy first.")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	// 1. Check if there's an active token and back it up
	activeToken, activeErr := getActiveToken()
	var activeEmail string
	if activeErr == nil && activeToken != nil {
		fmt.Println("Retrieving current session details...")
		email, err := FetchEmail(activeToken)
		if err == nil {
			activeEmail = email
			fmt.Printf("You are currently logged in as: %s\n", activeEmail)
			
			// Save it in the manager before logging out so it's not lost
			err = saveTokenForAccount(activeEmail, activeToken)
			if err != nil {
				fmt.Printf("Warning: failed to save current session to manager keyring: %v\n", err)
			} else {
				// Get label
				label := strings.Split(activeEmail, "@")[0]
				cfg.AddOrUpdateAccount(label, activeEmail)
				_ = SaveConfig(cfg)
				fmt.Printf("Current session auto-saved as label '%s'.\n", label)
			}
		} else {
			fmt.Printf("Warning: Could not fetch email for current active session: %v\n", err)
		}
	}

	// Prompt user for confirmation
	confirm := promptString("To add a new account, we will temporarily log you out of agy.\nContinue? [y/N]: ", "n")
	if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
		fmt.Println("Aborted.")
		os.Exit(0)
	}

	// 2. Delete the active token to force agy to prompt for login
	fmt.Println("Clearing current active session...")
	_ = deleteActiveToken()

	// 3. Start agy interactively
	fmt.Println("\nStarting agy to authenticate the new account.")
	fmt.Println("Please log in through the browser when prompted.")
	fmt.Println("----------------------------------------------------------------------")
	
	cmdExec := exec.Command("agy")
	cmdExec.Stdin = os.Stdin
	cmdExec.Stdout = os.Stdout
	cmdExec.Stderr = os.Stderr
	if err := cmdExec.Run(); err != nil {
		fmt.Printf("Warning: 'agy' command returned an error during authentication: %v\n", err)
	}
	
	fmt.Println("----------------------------------------------------------------------")

	// 4. Capture the new token
	newToken, err := getActiveToken()
	if err != nil || newToken == nil {
		fmt.Println("Error: No new active session found.")
		if activeToken != nil {
			fmt.Println("Restoring previous active session...")
			_ = saveActiveToken(activeToken)
		}
		os.Exit(1)
	}

	fmt.Println("Fetching details for the new account...")
	newEmail, err := FetchEmail(newToken)
	if err != nil {
		fmt.Printf("Error verifying new token: %v\n", err)
		if activeToken != nil {
			fmt.Println("Restoring previous active session...")
			_ = saveActiveToken(activeToken)
		}
		os.Exit(1)
	}

	fmt.Printf("Successfully authenticated as: %s\n", newEmail)

	// Ask user for a label
	defaultLabel := initialLabel
	if defaultLabel == "" {
		defaultLabel = strings.Split(newEmail, "@")[0]
	}
	labelPrompt := fmt.Sprintf("Enter a label for this account (default: '%s'): ", defaultLabel)
	label := promptString(labelPrompt, defaultLabel)

	// Save new token in manager keyring
	err = saveTokenForAccount(newEmail, newToken)
	if err != nil {
		fmt.Printf("Error saving token to keyring: %v\n", err)
		if activeToken != nil {
			fmt.Println("Restoring previous active session...")
			_ = saveActiveToken(activeToken)
		}
		os.Exit(1)
	}

	// Save to config
	cfg.AddOrUpdateAccount(label, newEmail)
	err = SaveConfig(cfg)
	if err != nil {
		fmt.Printf("Error saving config file: %v\n", err)
	}

	fmt.Printf("\nSaved account '%s' (%s) successfully.\n", label, newEmail)
	fmt.Println("This account is now the active account.")
}

func cmdSwitch(labelOrEmail string) {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	acc, found := cfg.GetAccount(labelOrEmail)
	if !found {
		fmt.Printf("Error: account matching '%s' not found.\n", labelOrEmail)
		fmt.Println("Available accounts:")
		cmdList()
		os.Exit(1)
	}

	token, err := getSavedToken(acc.Email)
	if err != nil {
		fmt.Printf("Error retrieving token for %s from keyring: %v\n", acc.Email, err)
		os.Exit(1)
	}

	// If the token is expired or close to expiry, refresh it before writing to active slot
	if token.Token.Expiry.Before(time.Now().Add(5 * time.Minute)) {
		fmt.Println("Token is expired or expiring soon. Refreshing...")
		err = RefreshAgyToken(token)
		if err != nil {
			fmt.Printf("Warning: Failed to refresh token: %v. Storing anyway, agy will attempt to refresh.\n", err)
		} else {
			// Save the refreshed token back in our manager keyring
			_ = saveTokenForAccount(acc.Email, token)
		}
	}

	err = saveActiveToken(token)
	if err != nil {
		fmt.Printf("Error writing active token to keyring: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Switched active agy account to: %s (%s)\n", acc.Email, acc.Label)
}

func cmdRemove(labelOrEmail string) {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	acc, found := cfg.GetAccount(labelOrEmail)
	if !found {
		fmt.Printf("Error: account matching '%s' not found.\n", labelOrEmail)
		os.Exit(1)
	}

	confirm := promptString(fmt.Sprintf("Are you sure you want to remove account '%s' (%s)? [y/N]: ", acc.Label, acc.Email), "n")
	if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
		fmt.Println("Aborted.")
		os.Exit(0)
	}

	// Delete from manager keyring
	_ = deleteSavedToken(acc.Email)

	// If this account was active, log out from agy as well
	activeToken, activeErr := getActiveToken()
	if activeErr == nil && activeToken != nil {
		savedToken, savedErr := getSavedToken(acc.Email)
		// We delete from active keyring if the refresh tokens match
		if savedErr == nil && savedToken != nil && savedToken.Token.RefreshToken == activeToken.Token.RefreshToken {
			fmt.Println("Logging out active session as it was deleted...")
			_ = deleteActiveToken()
		}
	}

	// Remove from config file
	cfg.RemoveAccount(acc.Email)
	_ = SaveConfig(cfg)

	fmt.Printf("Successfully removed account '%s' (%s).\n", acc.Label, acc.Email)
}

func cmdRename(oldLabel, newLabel string) {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	acc, found := cfg.GetAccount(oldLabel)
	if !found {
		fmt.Printf("Error: account with label '%s' not found.\n", oldLabel)
		os.Exit(1)
	}

	// Verify new label is not already taken
	if _, exists := cfg.GetAccount(newLabel); exists {
		fmt.Printf("Error: label '%s' is already in use by another account.\n", newLabel)
		os.Exit(1)
	}

	for i, a := range cfg.Accounts {
		if a.Email == acc.Email {
			cfg.Accounts[i].Label = newLabel
			break
		}
	}

	err = SaveConfig(cfg)
	if err != nil {
		fmt.Printf("Error saving config file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Renamed account '%s' to '%s' (%s).\n", oldLabel, newLabel, acc.Email)
}

func cmdCompletion(shell string) {
	bashScript := `_agy_manager_completion() {
    local cur prev opts cli_flag
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    opts="ls list add use switch rm remove rename completion help"

    # Detect if --cli flag was used (e.g. agy-manager --cli gemini use ...)
    cli_flag="agy"
    for ((i=1; i<COMP_CWORD; i++)); do
        if [[ "${COMP_WORDS[i]}" == "--cli" && $((i+1)) -lt COMP_CWORD ]]; then
            cli_flag="${COMP_WORDS[$((i+1))]}"
        fi
    done

    # Complete --cli value
    if [[ "$prev" == "--cli" ]]; then
        COMPREPLY=( $(compgen -W "agy gemini antigravity" -- "${cur}") )
        return 0
    fi

    # Complete first positional: allow --cli as a flag too
    if [[ ${COMP_CWORD} -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "--cli ${opts}" -- ${cur}) )
        return 0
    fi

    case "${prev}" in
        use|switch|rm|remove)
            local accounts
            accounts=$(${COMP_WORDS[0]} --cli ${cli_flag} _accounts 2>/dev/null)
            COMPREPLY=( $(compgen -W "${accounts}" -- ${cur}) )
            return 0
            ;;
        rename)
            local accounts
            accounts=$(${COMP_WORDS[0]} --cli ${cli_flag} _accounts 2>/dev/null)
            COMPREPLY=( $(compgen -W "${accounts}" -- ${cur}) )
            return 0
            ;;
        completion)
            COMPREPLY=( $(compgen -W "bash zsh" -- ${cur}) )
            return 0
            ;;
        --cli)
            COMPREPLY=( $(compgen -W "agy gemini antigravity" -- "${cur}") )
            return 0
            ;;
        gemini|agy|antigravity)
            # After --cli <value>, complete the command
            COMPREPLY=( $(compgen -W "${opts}" -- ${cur}) )
            return 0
            ;;
        *)
            ;;
    esac

    COMPREPLY=( $(compgen -W "${opts}" -- ${cur}) )
    return 0
}
complete -F _agy_manager_completion agy-manager
`

	zshScript := `#compdef agy-manager

_agy_manager() {
    local context state state_descr line cli_flag
    typeset -A opt_args

    # Detect --cli flag value from already-typed words
    cli_flag="agy"
    for ((i=2; i<=$#words-1; i++)); do
        if [[ "${words[i]}" == "--cli" ]]; then
            cli_flag="${words[$((i+1))]}"
        fi
    done

    _arguments \
        '--cli[Target CLI tool]:cli:(agy antigravity gemini)' \
        '1: :->command' \
        '*: :->args'

    case $state in
        command)
            local -a commands
            commands=(
                'ls:List all saved accounts and active status'
                'list:List all saved accounts and active status'
                'add:Add a new Google account interactively'
                'use:Switch the active account'
                'switch:Switch the active account'
                'rm:Remove a saved account'
                'remove:Remove a saved account'
                'rename:Rename an account'\''s label'
                'completion:Print shell completion script'
                'help:Show help message'
            )
            _describe -t commands 'agy-manager commands' commands
            ;;
        args)
            case $words[2] in
                use|switch|rm|remove)
                    if [[ $CURRENT -eq 3 ]]; then
                        local -a accounts
                        accounts=(${(f)"$($words[1] --cli ${cli_flag} _accounts 2>/dev/null)"})
                        _describe -t accounts 'accounts' accounts
                    fi
                    ;;
                rename)
                    if [[ $CURRENT -eq 3 ]]; then
                        local -a accounts
                        accounts=(${(f)"$($words[1] --cli ${cli_flag} _accounts 2>/dev/null)"})
                        _describe -t accounts 'accounts' accounts
                    fi
                    ;;
                completion)
                    local -a shells
                    shells=(
                        'bash:Bash completion'
                        'zsh:Zsh completion'
                    )
                    _describe -t shells 'shells' shells
                    ;;
            esac
            ;;
    esac
}

_agy_manager "$@"
`

	switch shell {
	case "bash":
		fmt.Print(bashScript)
	case "zsh":
		fmt.Print(zshScript)
	default:
		fmt.Printf("Unsupported shell: %s. Supported shells are bash, zsh.\n", shell)
		os.Exit(1)
	}
}

func cmdAccounts() {
	cfg, err := LoadConfig()
	if err != nil {
		return
	}
	for _, acc := range cfg.Accounts {
		fmt.Println(acc.Email)
	}
}

