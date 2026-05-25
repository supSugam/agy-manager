# agy-manager

A command-line utility to manage and switch between multiple Google accounts for `agy`, `gemini-cli`, and `Antigravity IDE`. 

The tool stores OAuth2 tokens in the system keyring and automates the process of swapping active credentials, eliminating the need for repeated browser-based authentication.

## Installation

### Prerequisites
- Go 1.20+
- Linux with a system keyring (libsecret, GNOME Keyring, or KWallet)

### Build and Install
```bash
git clone https://github.com/ctrlcat/agy-manager.git
cd agy-manager
./install.sh
```
This builds the binary to `~/.local/bin/` and sets up shell completions for Bash and Zsh.

## Usage

### Commands

| Command | Description |
| :--- | :--- |
| `ls` | List all saved accounts and the currently active one. |
| `add [label]` | Add a new account. Backs up current session, triggers login, and saves credentials. |
| `use <label\|email>` | Switch the active account. Refreshes tokens automatically if expired. |
| `rm <label\|email>` | Remove a saved account from the manager and keyring. |
| `rename <old> <new>` | Rename an account label. |
| `completion <shell>` | Generate shell completion scripts (bash/zsh). |

### Global Options
- `--cli <name>`: Target a specific tool. Supported: `agy` (default), `gemini`, `antigravity`.

## Examples

### Antigravity CLI (Default)
```bash
# Add current session as 'work'
agy-manager add work

# Switch to 'personal'
agy-manager use personal
```

### Gemini CLI
```bash
# Add a Gemini CLI account
agy-manager --cli gemini add research

# Switch Gemini account
agy-manager --cli gemini use research
```

### Antigravity IDE
```bash
# Add current IDE session
agy-manager --cli antigravity add dev-env

# Switch IDE session (requires IDE restart)
agy-manager --cli antigravity use dev-env
```

### Custom OAuth Credentials
If the official tools update their client IDs or you wish to use your own, you can override them via environment variables or the configuration file:

**Environment Variables:**
- `AGY_CLIENT_ID` / `AGY_CLIENT_SECRET`
- `GEMINI_CLIENT_ID` / `GEMINI_CLIENT_SECRET`
- `ANTIGRAVITY_CLIENT_ID` / `ANTIGRAVITY_CLIENT_SECRET`

**Configuration File (`~/.config/agy-manager/<cli>.json`):**
```json
{
  "client_id": "your-client-id",
  "client_secret": "your-client-secret",
  "accounts": [...]
}
```

## Technical Details

### Storage and Security
- **OAuth2 Tokens**: Stored in the system keyring (`gemini`, `agy-manager`, `agy-manager-gemini`, or `agy-manager-antigravity` services).
- **Configuration**: Metadata (labels, emails, last used) is stored in `~/.config/agy-manager/<cli>.json`. No secrets are stored in these files.
- **Token Refresh**: The tool includes an internal OAuth2 client to refresh tokens using the same client IDs as the target tools.

### IDE Integration
For `Antigravity IDE`, the tool modifies the SQLite state database located at `~/.config/Antigravity/User/globalStorage/state.vscdb`.

## Shell Completion

Add the following to your shell configuration:

**Bash (`~/.bashrc`):**
```bash
source <(agy-manager completion bash)
```

**Zsh (`~/.zshrc`):**
```bash
source <(agy-manager completion zsh)
```
