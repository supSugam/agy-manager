package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	clientIDRegex     = regexp.MustCompile(`[0-9]{10,}-[a-z0-9]{10,}\.apps\.googleusercontent\.com`)
	clientSecretRegex = regexp.MustCompile(`GOCSPX-[A-Za-z0-9_-]{24,40}`)
)

func validateCredentials(id, secret string) bool {
	return clientIDRegex.MatchString(id) && clientSecretRegex.MatchString(secret)
}

func DiscoverAgyCredentials() (string, string) {
	path, err := exec.LookPath("agy")
	if err != nil {
		return "", ""
	}

	var data []byte
	if _, err := exec.LookPath("strings"); err == nil {
		cmd := exec.Command("strings", path)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			data = out.Bytes()
		}
	}

	if data == nil {
		data, _ = os.ReadFile(path)
	}

	if data != nil {
		id, secret := extractFromBuffer(data, "1071006060591")
		if validateCredentials(id, secret) {
			return id, secret
		}
	}

	return "", ""
}

func DiscoverGeminiCredentials() (string, string) {
	path, err := exec.LookPath("gemini")
	if err != nil {
		return "", ""
	}

	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		realPath = path
	}

	bundleDir := filepath.Dir(realPath)
	if strings.Contains(realPath, "node_modules") {
		parts := strings.Split(realPath, "node_modules")
		if len(parts) > 1 {
			pkgPath := filepath.Join(parts[0], "node_modules", "@google", "gemini-cli")
			if _, err := os.Stat(pkgPath); err == nil {
				bundleDir = filepath.Join(pkgPath, "bundle")
			}
		}
	}

	var combinedOut bytes.Buffer
	if _, err := exec.LookPath("grep"); err == nil {
		cmd := exec.Command("grep", "-rhE", "[0-9]+-[a-z0-9]+\\.apps\\.googleusercontent\\.com|GOCSPX-[A-Za-z0-9_-]+", bundleDir)
		cmd.Stdout = &combinedOut
		_ = cmd.Run()
	}

	if combinedOut.Len() == 0 {
		_ = filepath.Walk(bundleDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, ".js") {
				if d, err := os.ReadFile(path); err == nil {
					combinedOut.Write(d)
					combinedOut.WriteByte('\n')
				}
			}
			return nil
		})
	}

	id, secret := extractFromBuffer(combinedOut.Bytes(), "681255809395")
	if validateCredentials(id, secret) {
		return id, secret
	}

	return "", ""
}

func extractFromBuffer(data []byte, preferredPrefix string) (string, string) {
	content := string(data)
	
	// 1. Find all Client IDs
	var clientID string
	idMatches := clientIDRegex.FindAllString(content, -1)
	for _, m := range idMatches {
		if preferredPrefix != "" && strings.HasPrefix(m, preferredPrefix) {
			clientID = m
			break
		}
	}
	if clientID == "" && len(idMatches) > 0 {
		clientID = idMatches[0]
	}

	// 2. Find all Secrets and handle overlap/concatenation
	var clientSecret string
	secretMatches := clientSecretRegex.FindAllString(content, -1)
	for _, m := range secretMatches {
		// If the match contains "GOCSPX-" again after the first 7 chars, truncate it
		if idx := strings.Index(m[7:], "GOCSPX-"); idx != -1 {
			m = m[:idx+7]
		}
		
		// Heuristic: for 'agy', we know it currently starts with GOCSPX-K58F...
		// If we're looking for agy and this matches, prioritize it.
		if preferredPrefix == "1071006060591" && strings.HasPrefix(m, "GOCSPX-K58F") {
			clientSecret = m
			break
		}
		if clientSecret == "" {
			clientSecret = m
		}
	}

	return clientID, clientSecret
}
