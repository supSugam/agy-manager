package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigLoadSave(t *testing.T) {
	// Use temp config directory
	tmpDir, err := os.MkdirTemp("", "agy-manager-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Override config path function or environment variable
	// Wait, getConfigPath uses os.UserConfigDir().
	// We can set XDG_CONFIG_HOME or HOME to force UserConfigDir to point to our temp dir.
	oldHome := os.Getenv("HOME")
	oldXdg := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("XDG_CONFIG_HOME", oldXdg)
	}()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if len(cfg.Accounts) != 0 {
		t.Errorf("expected 0 accounts, got %d", len(cfg.Accounts))
	}

	cfg.AddOrUpdateAccount("test-label", "test@example.com")
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	// Verify file was created
	expectedPath := filepath.Join(tmpDir, "agy-manager", "agy.json")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatalf("expected config file at %s, but it does not exist", expectedPath)
	}

	// Reload
	cfg2, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig reload failed: %v", err)
	}

	if len(cfg2.Accounts) != 1 {
		t.Fatalf("expected 1 account after reload, got %d", len(cfg2.Accounts))
	}

	acc, found := cfg2.GetAccount("test-label")
	if !found {
		t.Fatalf("expected to find account by label")
	}

	if acc.Email != "test@example.com" {
		t.Errorf("expected email test@example.com, got %s", acc.Email)
	}

	acc2, found := cfg2.GetAccount("test@example.com")
	if !found {
		t.Fatalf("expected to find account by email")
	}

	if acc2.Label != "test-label" {
		t.Errorf("expected label test-label, got %s", acc2.Label)
	}

	// Remove
	removed := cfg2.RemoveAccount("test-label")
	if !removed {
		t.Errorf("expected RemoveAccount to return true")
	}

	if len(cfg2.Accounts) != 0 {
		t.Errorf("expected 0 accounts after remove, got %d", len(cfg2.Accounts))
	}
}
