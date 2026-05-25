#!/bin/bash
set -e

# Ensure we're in the repository root directory
cd "$(dirname "$0")"

echo "Building agy-manager..."
go build -o agy-manager

echo "Installing binary to ~/.local/bin..."
mkdir -p ~/.local/bin
cp agy-manager ~/.local/bin/

echo "Installing completion scripts..."

# 1. Install Bash completion
BASH_COMP_DIR="${BASH_COMP_DIR:-$HOME/.local/share/bash-completion/completions}"
mkdir -p "$BASH_COMP_DIR"
./agy-manager completion bash > "$BASH_COMP_DIR/agy-manager"
echo "✓ Bash completions installed to: $BASH_COMP_DIR/agy-manager"

# 2. Install Zsh completion (standard user-level site-functions)
ZSH_COMP_DIR="${ZSH_COMP_DIR:-$HOME/.local/share/zsh/site-functions}"
mkdir -p "$ZSH_COMP_DIR"
./agy-manager completion zsh > "$ZSH_COMP_DIR/_agy-manager"
echo "✓ Zsh completions installed to: $ZSH_COMP_DIR/_agy-manager"

# 3. Also support Oh My Zsh if present
if [ -d "$HOME/.oh-my-zsh" ]; then
    OMZ_COMP_DIR="$HOME/.oh-my-zsh/custom/completions"
    mkdir -p "$OMZ_COMP_DIR"
    ./agy-manager completion zsh > "$OMZ_COMP_DIR/_agy-manager"
    echo "✓ Zsh completions also installed to: $OMZ_COMP_DIR/_agy-manager"
fi

echo ""
echo "Installation successful!"
echo "Please restart your shell or run 'source ~/.bashrc' (Bash) / 'exec zsh' (Zsh) to activate."
