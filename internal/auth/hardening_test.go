package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/xapi"
)

func syntheticConfig() config.Config {
	c := config.Default()
	c.ClientID, c.AccessToken, c.RefreshToken = "synthetic-app", "synthetic-old-access", "synthetic-old-refresh"
	c.TokenType, c.Scope, c.ScopeSource = "Bearer", xapi.RequiredScope, "response"
	c.UserID = "123"
	return c
}
func tokenResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

const rotatedResponse = `{"access_token":"synthetic-new-access","refresh_token":"synthetic-new-refresh","expires_in":3600,"token_type":"bearer"}`

func assertRestartBlocked(t *testing.T, path string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRefreshRestartHelper$")
	cmd.Env = append(os.Environ(), "XSTASH_TEST_REFRESH_PATH="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restart check failed: %v %s", err, output)
	}
}
func TestRefreshRestartHelper(t *testing.T) {
	path := os.Getenv("XSTASH_TEST_REFRESH_PATH")
	if path == "" {
		t.Skip("subprocess helper")
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: rt(func(*http.Request) (*http.Response, error) {
		t.Fatal("pending token was replayed after restart")
		return nil, nil
	})}
	if err := refresh(context.Background(), &c, path, client, "https://synthetic.invalid/token"); err == nil || !strings.Contains(err.Error(), "unfinished refresh") {
		t.Fatalf("restart was not blocked: %v", err)
	}
	if err := Ready(c); err == nil {
		t.Fatal("pending state accepted for paid reads")
	}
}

func TestRefreshFailurePersistsStopAcrossRestart(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		transportErr bool
		body         string
	}{
		{name: "400", status: 400}, {name: "401", status: 401}, {name: "429", status: 429}, {name: "500", status: 500},
		{name: "response lost", transportErr: true}, {name: "truncated response", status: 200, body: `{"access_token":"synthetic-new-access"`},
		{name: "invalid type", status: 200, body: `{"access_token":"synthetic-new-access","expires_in":3600,"token_type":"unexpected"}`},
		{name: "scope reduced", status: 200, body: `{"access_token":"synthetic-new-access","expires_in":3600,"token_type":"Bearer","scope":"tweet.read"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			c := syntheticConfig()
			if err := config.Save(path, c); err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := &http.Client{Transport: rt(func(*http.Request) (*http.Response, error) {
				calls++
				saved, err := config.Load(path)
				if err != nil || saved.RefreshState != "pending" || saved.RefreshStartedAt.IsZero() {
					t.Fatal("request sent before durable stop marker")
				}
				if tc.transportErr {
					return nil, errors.New("synthetic-old-refresh must not leak")
				}
				resp := tokenResponse(tc.body)
				resp.StatusCode = tc.status
				return resp, nil
			})}
			err := refresh(context.Background(), &c, path, client, "https://synthetic.invalid/token")
			if err == nil || strings.Contains(err.Error(), "synthetic-old-refresh") || strings.Contains(err.Error(), "synthetic-new-access") {
				t.Fatalf("unsafe error: %v", err)
			}
			if err := refresh(context.Background(), &c, path, client, "https://synthetic.invalid/token"); err == nil {
				t.Fatal("same-process retry accepted")
			}
			if calls != 1 || c.AccessToken != "synthetic-old-access" || c.RefreshToken != "synthetic-old-refresh" || c.RefreshState != "pending" {
				t.Fatal("unsafe refresh failure state")
			}
			assertRestartBlocked(t, path)
		})
	}
}

func TestRefreshPreflightFailureSendsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	c := syntheticConfig()
	before := c
	client := &http.Client{Transport: rt(func(*http.Request) (*http.Response, error) {
		t.Fatal("request sent despite failed persistence preflight")
		return nil, nil
	})}
	if err := refresh(context.Background(), &c, path, client, "https://synthetic.invalid/token"); err == nil {
		t.Fatal("preflight succeeded")
	}
	if c != before {
		t.Fatal("preflight failure changed in-memory credentials")
	}
}

func TestRefreshFinalSaveFailureBlocksReplay(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("requires non-root Unix file permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := syntheticConfig()
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	client := &http.Client{Transport: rt(func(*http.Request) (*http.Response, error) {
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatal(err)
		}
		return tokenResponse(rotatedResponse), nil
	})}
	if err := refresh(context.Background(), &c, path, client, "https://synthetic.invalid/token"); err == nil {
		t.Fatal("save should fail")
	}
	if c.RefreshState != "pending" || c.RefreshToken != "synthetic-old-refresh" {
		t.Fatal("failed save published unpersisted credentials")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load(path)
	if err != nil || saved.RefreshState != "pending" || saved.RefreshToken != "synthetic-old-refresh" {
		t.Fatal("durable stop marker lost")
	}
	assertRestartBlocked(t, path)
}

func TestRefreshPreservesOrReplacesKnownGrant(t *testing.T) {
	for _, omitRefresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "rotation", true: "no rotation"}[omitRefresh], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			c := syntheticConfig()
			original := c
			body := rotatedResponse
			if omitRefresh {
				body = `{"access_token":"synthetic-new-access","expires_in":3600,"token_type":"Bearer"}`
			}
			client := &http.Client{Transport: rt(func(*http.Request) (*http.Response, error) { return tokenResponse(body), nil })}
			if err := refresh(context.Background(), &c, path, client, "https://synthetic.invalid/token"); err != nil {
				t.Fatal(err)
			}
			if c.RefreshState != "" || !c.RefreshStartedAt.IsZero() || c.Scope != original.Scope || c.ScopeSource != original.ScopeSource || c.UserID != original.UserID || c.ExpiresAt.Before(time.Now()) {
				t.Fatal("refresh lost metadata")
			}
			if omitRefresh && c.RefreshToken != original.RefreshToken {
				t.Fatal("non-rotating grant lost refresh token")
			}
			saved, err := config.Load(path)
			if err != nil || saved != c {
				t.Fatal("memory/disk mismatch")
			}
		})
	}
}

func TestLoginMetadataAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		scope   *string
		refresh string
		wantErr bool
		source  string
	}{
		{name: "omitted scope inherits requested", refresh: "synthetic-new-refresh", source: "requested"},
		{name: "explicit scope", scope: strptr(xapi.RequiredScope), refresh: "synthetic-new-refresh", source: "response"},
		{name: "insufficient scope", scope: strptr("tweet.read"), refresh: "synthetic-new-refresh", wantErr: true},
		{name: "unexpected write scope", scope: strptr(xapi.RequiredScope + " tweet.write"), refresh: "synthetic-new-refresh", wantErr: true},
		{name: "empty scope", scope: strptr(""), refresh: "synthetic-new-refresh", wantErr: true},
		{name: "missing offline token", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			c := syntheticConfig()
			c.RefreshState = "pending"
			c.RefreshStartedAt = time.Now().UTC()
			before := c
			if err := config.Save(path, c); err != nil {
				t.Fatal(err)
			}
			token := xapi.OAuthToken{AccessToken: "synthetic-new-access", RefreshToken: tc.refresh, TokenType: "Bearer", Scope: tc.scope, ExpiresIn: 3600}
			err := saveLogin(&c, path, token)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected result: %v", err)
			}
			if tc.wantErr {
				if c != before {
					t.Fatal("invalid login changed state")
				}
				return
			}
			if c.RefreshState != "" || !c.RefreshStartedAt.IsZero() || c.Scope != xapi.RequiredScope || c.ScopeSource != tc.source || c.UserID != "" || c.OwnedEligible {
				t.Fatal("login recovery metadata incorrect")
			}
			saved, err := config.Load(path)
			if err != nil || saved != c {
				t.Fatal("login not durable")
			}
		})
	}
}
func strptr(v string) *string { return &v }

func TestReadyRejectsUnverifiedLegacyMetadata(t *testing.T) {
	c := syntheticConfig()
	c.Scope = ""
	if Ready(c) == nil {
		t.Fatal("unknown scope accepted")
	}
	c = syntheticConfig()
	c.TokenType = ""
	if Ready(c) == nil {
		t.Fatal("unknown type accepted")
	}
}

func TestLoginSaveFailurePreservesPendingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	c := syntheticConfig()
	c.RefreshState = "pending"
	before := c
	token := xapi.OAuthToken{AccessToken: "synthetic-new-access", RefreshToken: "synthetic-new-refresh", TokenType: "Bearer", ExpiresIn: 3600}
	if err := saveLogin(&c, path, token); err == nil {
		t.Fatal("login save unexpectedly succeeded")
	}
	if c != before {
		t.Fatal("failed login save cleared pending state or published unpersisted credentials")
	}
}
