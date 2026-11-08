package xapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const RequiredScope = "tweet.read users.read bookmark.read like.read offline.access"

type OAuthToken struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    int64   `json:"expires_in"`
	TokenType    string  `json:"token_type"`
	Scope        *string `json:"scope"`
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func PKCE() (state, verifier, challenge string, err error) {
	state, err = randomToken()
	if err != nil {
		return
	}
	verifier, err = randomToken()
	if err != nil {
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}
func AuthorizationURL(clientID, redirect, state, challenge string) string {
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "scope": {RequiredScope}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return "https://x.com/i/oauth2/authorize?" + q.Encode()
}
func Exchange(ctx context.Context, client *http.Client, endpoint, clientID, secret string, values url.Values) (OAuthToken, error) {
	var t OAuthToken
	values.Set("client_id", clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return t, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if secret != "" {
		req.SetBasicAuth(clientID, secret)
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := safeClient.Do(req)
	if err != nil {
		return t, fmt.Errorf("OAuth token request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return t, fmt.Errorf("OAuth token endpoint returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return t, fmt.Errorf("invalid OAuth token response")
	}
	if err = json.Unmarshal(body, &t); err != nil {
		return t, fmt.Errorf("invalid OAuth token response")
	}
	if t.AccessToken == "" || t.ExpiresIn <= 0 || t.ExpiresIn > int64((1<<63-1)/time.Second) {
		return t, fmt.Errorf("incomplete OAuth token response")
	}
	if !strings.EqualFold(t.TokenType, "Bearer") {
		return t, fmt.Errorf("OAuth token response requires Bearer token_type")
	}
	return t, nil
}
