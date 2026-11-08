package xapi

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func TestPKCEAndRefresh(t *testing.T) {
	state, verifier, challenge, err := PKCE()
	if err != nil || len(state) < 32 || len(verifier) < 43 || len(challenge) < 43 {
		t.Fatal("invalid PKCE output")
	}
	u, err := url.Parse(AuthorizationURL("client", "http://127.0.0.1:1234/callback", state, challenge))
	if err != nil || u.Query().Get("state") != state || u.Query().Get("code_challenge") != challenge {
		t.Fatal("invalid authorization URL")
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "old" {
			t.Error("invalid refresh request")
		}
		return response(200, `{"access_token":"new","refresh_token":"rotated","expires_in":3600}`), nil
	})}
	token, err := Exchange(context.Background(), client, "https://example.invalid/token", "client", "", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"old"}})
	if err != nil || token.RefreshToken != "rotated" {
		t.Fatalf("refresh: %v", err)
	}
}
func TestOAuthDoesNotForwardCredentialsOnRedirect(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		r := response(307, "")
		r.Header.Set("Location", "https://other.invalid/token")
		return r, nil
	})}
	if _, err := Exchange(context.Background(), client, "https://example.invalid/token", "id", "secret", url.Values{}); err == nil || calls != 1 {
		t.Fatalf("redirect followed: calls=%d err=%v", calls, err)
	}
}
