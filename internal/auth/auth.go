package auth

import (
	"context"
	"fmt"
	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"net"
	"net/http"
	"net/url"
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
		c.AccessToken = t.AccessToken
		c.RefreshToken = t.RefreshToken
		c.ExpiresAt = time.Now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second)
		c.UserID = ""
		c.OwnedEligible = false
		return config.Save(configPath, *c)
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
	if c.RefreshToken == "" {
		return fmt.Errorf("refresh token missing; authenticate again")
	}
	t, err := xapi.Exchange(ctx, client, endpoint, c.ClientID, c.ClientSecret, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}})
	if err != nil {
		return err
	}
	c.AccessToken = t.AccessToken
	if t.RefreshToken != "" {
		c.RefreshToken = t.RefreshToken
	}
	c.ExpiresAt = time.Now().UTC().Add(time.Duration(t.ExpiresIn) * time.Second)
	return config.Save(path, *c)
}
