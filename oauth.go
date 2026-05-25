package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type OAuthToken struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

type AgyToken struct {
	Token      OAuthToken `json:"token"`
	AuthMethod string     `json:"auth_method"`
}

type GoogleTokeninfo struct {
	Email string `json:"email"`
	Exp   string `json:"exp"`
}

type GoogleTokenRefreshResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	TokenType   string `json:"token_type"`
	IDToken     string `json:"id_token"`
}

func RefreshAgyToken(token *AgyToken) error {
	if token.Token.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	clientID, clientSecret := cfg.GetCredentials("agy")

	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", token.Token.RefreshToken)

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("oauth refresh request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var refreshResponse GoogleTokenRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&refreshResponse); err != nil {
		return err
	}

	token.Token.AccessToken = refreshResponse.AccessToken
	if refreshResponse.ExpiresIn > 0 {
		token.Token.Expiry = time.Now().Add(time.Duration(refreshResponse.ExpiresIn) * time.Second)
	} else {
		token.Token.Expiry = time.Now().Add(time.Hour)
	}

	return nil
}

func FetchEmail(token *AgyToken) (string, error) {
	// If token has expired or is expiring soon (within 5 minutes), refresh it first
	if token.Token.Expiry.Before(time.Now().Add(5 * time.Minute)) {
		if err := RefreshAgyToken(token); err != nil {
			return "", fmt.Errorf("failed to refresh token before fetching email: %w", err)
		}
	}

	reqUrl := fmt.Sprintf("https://oauth2.googleapis.com/tokeninfo?access_token=%s", url.QueryEscape(token.Token.AccessToken))
	resp, err := http.Get(reqUrl)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("tokeninfo request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var info GoogleTokeninfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}

	if info.Email == "" {
		return "", fmt.Errorf("no email found in tokeninfo response")
	}

	return info.Email, nil
}
