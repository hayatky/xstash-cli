package auth

import (
	"context"
	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRefreshPersistsRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := config.Config{ClientID: "client", AccessToken: "old", RefreshToken: "old-refresh", Scope: xapi.RequiredScope, TokenType: "Bearer"}
	client := &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
		if r.FormValue("refresh_token") != "old-refresh" {
			t.Error("wrong token")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"new","refresh_token":"rotated","expires_in":3600,"token_type":"bearer"}`)), Header: make(http.Header)}, nil
	})}
	if e := refresh(context.Background(), &c, path, client, "https://example.invalid/token"); e != nil {
		t.Fatal(e)
	}
	got, e := config.Load(path)
	if e != nil || got.RefreshToken != "rotated" || got.AccessToken != "new" {
		t.Fatalf("rotation: %v", e)
	}
	st, e := os.Stat(path)
	if e != nil || st.Mode().Perm()&0077 != 0 {
		t.Fatal("unprotected config")
	}
}

func TestCallbackRequiresState(t *testing.T) {
	codes := make(chan string, 1)
	handler := callback("expected", codes)
	for _, tt := range []struct {
		url    string
		status int
	}{{"/callback?state=wrong&code=x", 400}, {"/callback?state=expected", 400}, {"/other?state=expected&code=x", 404}, {"/callback?state=expected&code=ok", 200}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", tt.url, nil))
		if w.Code != tt.status {
			t.Fatalf("%s: %d", tt.url, w.Code)
		}
	}
	select {
	case v := <-codes:
		if v != "ok" {
			t.Fatal(v)
		}
	default:
		t.Fatal("code not delivered")
	}
}
