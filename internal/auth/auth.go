package auth

import (
	"context"
	"fmt"
	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const tokenURL = "https://api.x.com/2/oauth2/token"

func Login(ctx context.Context, c *config.Config, configPath string, port int, show func(string)) error {
	if c.ClientID == "" {
		return fmt.Errorf("client ID is required")
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	defer ln.Close()
	state, verifier, challenge, err := xapi.PKCE()
	if err != nil {
		return err
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	code := make(chan string, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second}
	server.Handler = callback(state, code)
	defer server.Close()
	go server.Serve(ln)
	show(xapi.AuthorizationURL(c.ClientID, redirect, state, challenge))
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case v := <-code:
		t, err := xapi.Exchange(ctx, &http.Client{Timeout: 30 * time.Second}, tokenURL, c.ClientID, c.ClientSecret, url.Values{"grant_type": {"authorization_code"}, "code": {v}, "redirect_uri": {redirect}, "code_verifier": {verifier}})
		if err != nil {
			return err
		}
		return saveLogin(c, configPath, t)
	}
}
func callback(state string, code chan<- string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid state", http.StatusBadRequest)
			return
		}
		v := r.URL.Query().Get("code")
		if v == "" {
			http.Error(w, "Authorization failed", http.StatusBadRequest)
			return
		}
		select {
		case code <- v:
		default:
		}
		fmt.Fprint(w, "Authorization received. You may close this window.")
	})
}
func Refresh(ctx context.Context, c *config.Config, path string) error {
	return refresh(ctx, c, path, &http.Client{Timeout: 30 * time.Second}, tokenURL)
}
func refresh(ctx context.Context, c *config.Config, path string, client *http.Client, endpoint string) error {
	if c.RefreshState != "" {
		return fmt.Errorf("automatic refresh stopped after an unfinished refresh; authenticate again with config init")
	}
	if c.RefreshToken == "" {
		return fmt.Errorf("refresh token missing; authenticate again")
	}
	if c.ClientID == "" {
		return fmt.Errorf("client ID is required")
	}
	// The CLI holds the config lock from load through this transaction. Persist a
	// stop marker BEFORE sending a possibly single-use token. A crash, uncertain
	// response, or failed final save must never replay it on the next invocation.
	pending := *c
	pending.RefreshState = "pending"
	pending.RefreshStartedAt = time.Now().UTC()
	if err := config.Save(path, pending); err != nil {
		return fmt.Errorf("could not persist refresh stop marker; no token request sent: %w", err)
	}
	*c = pending
	t, err := xapi.Exchange(ctx, client, endpoint, c.ClientID, c.ClientSecret, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}})
	if err != nil {
		return fmt.Errorf("%w; automatic refresh stopped; authenticate again with config init", err)
	}
	next := *c
	next.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		next.RefreshToken = t.RefreshToken
	}
	next.TokenType = "Bearer"
	if t.Scope != nil {
		next.Scope = *t.Scope
		next.ScopeSource = "response"
	}
	if err := validateScopes(next.Scope); err != nil {
		return fmt.Errorf("%w; automatic refresh stopped", err)
	}
	next.ExpiresAt = time.Now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second)
	next.RefreshState = ""
	next.RefreshStartedAt = time.Time{}
	if err := config.Save(path, next); err != nil {
		return fmt.Errorf("could not persist refreshed credentials; automatic refresh stopped; authenticate again with config init: %w", err)
	}
	*c = next
	return nil
}

// Ready checks durable authorization metadata before any paid API request.
func Ready(c config.Config) error {
	if c.RefreshState != "" {
		return fmt.Errorf("automatic sync stopped after an unfinished refresh; authenticate again with config init")
	}
	if !strings.EqualFold(c.TokenType, "Bearer") {
		return fmt.Errorf("saved token type is unverified; authenticate again with config init")
	}
	return validateScopes(c.Scope)
}

func validateScopes(scope string) error {
	required := map[string]bool{}
	for _, v := range strings.Fields(xapi.RequiredScope) {
		required[v] = true
	}
	granted := map[string]bool{}
	for _, v := range strings.Fields(scope) {
		if !required[v] {
			return fmt.Errorf("OAuth grant includes an unexpected scope; authenticate again with the requested read-only scopes")
		}
		granted[v] = true
	}
	for v := range required {
		if !granted[v] {
			return fmt.Errorf("required OAuth scopes are missing or unverified; authenticate again with config init")
		}
	}
	return nil
}

func saveLogin(c *config.Config, path string, t xapi.OAuthToken) error {
	if t.RefreshToken == "" {
		return fmt.Errorf("OAuth response has no refresh token for offline access; authenticate again")
	}
	next := *c
	next.AccessToken, next.RefreshToken, next.TokenType = t.AccessToken, t.RefreshToken, t.TokenType
	// RFC 6749 permits omission when granted scope equals the requested scope.
	next.Scope, next.ScopeSource = xapi.RequiredScope, "requested"
	if t.Scope != nil {
		next.Scope, next.ScopeSource = *t.Scope, "response"
	}
	next.RefreshState, next.RefreshStartedAt = "", time.Time{}
	if err := Ready(next); err != nil {
		return err
	}
	next.ExpiresAt = time.Now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second)
	next.UserID, next.OwnedEligible = "", false
	if err := config.Save(path, next); err != nil {
		return fmt.Errorf("could not persist login credentials: %w", err)
	}
	*c = next
	return nil
}
