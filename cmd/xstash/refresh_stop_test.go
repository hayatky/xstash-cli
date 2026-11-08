package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/store"
	"github.com/hayatky/xstash-cli/internal/xapi"
)

type stopTransport func(*http.Request) (*http.Response, error)

func (f stopTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSyncStopsAllPhasesAfterRefreshFailure(t *testing.T) {
	for _, preflight := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP failure", true: "preflight persistence failure"}[preflight], func(t *testing.T) {
			if preflight && (runtime.GOOS == "windows" || os.Getuid() == 0) {
				t.Skip("requires non-root Unix permissions")
			}
			dir := t.TempDir()
			configDir := filepath.Join(dir, "config")
			if err := os.Mkdir(configDir, 0700); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(configDir, 0700)
			path := filepath.Join(configDir, "config.json")
			dbpath := filepath.Join(dir, "synthetic.db")
			c := config.Default()
			c.ClientID = "synthetic-app"
			c.AccessToken = "synthetic-old"
			c.RefreshToken = "synthetic-refresh"
			c.TokenType = "Bearer"
			c.Scope = xapi.RequiredScope
			c.ScopeSource = "response"
			c.MediaMicrousd = 5000
			c.UserID = "123"
			c.ExpiresAt = time.Now().Add(time.Hour)
			if err := config.Save(path, c); err != nil {
				t.Fatal(err)
			}
			s, err := store.Open(dbpath)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			posts := []store.Post{{ID: "1", Text: "synthetic", References: []store.Reference{{ID: "2", Type: "quoted"}}}}
			if _, err := s.SavePage(context.Background(), "like", posts, store.Now()); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveIncludes(context.Background(), nil, []json.RawMessage{json.RawMessage(`{"media_key":"synthetic","type":"photo","url":"https://pbs.twimg.com/synthetic.jpg"}`)}, store.Now()); err != nil {
				t.Fatal(err)
			}
			original := http.DefaultTransport
			defer func() { http.DefaultTransport = original }()
			calls, tokenCalls := 0, 0
			http.DefaultTransport = stopTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path == "/2/oauth2/token" {
					tokenCalls++
					return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
				}
				if calls != 1 || r.URL.Path != "/2/users/123/bookmarks" {
					t.Fatalf("unexpected later request: %s", r.URL.Path)
				}
				if preflight {
					if err := os.Chmod(configDir, 0500); err != nil {
						t.Fatal(err)
					}
				}
				return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
			})
			var out, errout bytes.Buffer
			err = syncCommand(context.Background(), []string{"--kind", "both", "--media"}, &out, &errout, s, &c, path, dbpath)
			if err == nil {
				t.Fatal("failed authentication did not stop sync")
			}
			wantCalls, wantTokens := 2, 1
			if preflight {
				wantCalls, wantTokens = 1, 0
			}
			if calls != wantCalls || tokenCalls != wantTokens {
				t.Fatalf("wrong request counts: %d %d", calls, tokenCalls)
			}
			if _, err := os.Stat(filepath.Join(dir, "media")); !os.IsNotExist(err) {
				t.Fatal("media phase ran after auth failure")
			}
			var quoteStatus, metadataReason, runStatus string
			if err := s.DB.QueryRow(`SELECT status FROM quote_queue WHERE id='2'`).Scan(&quoteStatus); err != nil || quoteStatus != "pending" {
				t.Fatal("quote phase ran after auth failure")
			}
			if err := s.DB.QueryRow(`SELECT reason FROM metadata_queue WHERE id='1'`).Scan(&metadataReason); err != nil || metadataReason != "pending" {
				t.Fatal("metadata phase ran after auth failure")
			}
			if err := s.DB.QueryRow(`SELECT status FROM sync_runs`).Scan(&runStatus); err != nil || runStatus != "failed" {
				t.Fatal("failed sync status not recorded")
			}
		})
	}
}

func TestSyncPendingMarkerStopsEvenValidAccessToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	dbpath := filepath.Join(dir, "db")
	c := config.Default()
	c.AccessToken = "synthetic"
	c.RefreshToken = "synthetic-refresh"
	c.TokenType = "Bearer"
	c.Scope = xapi.RequiredScope
	c.RefreshState = "pending"
	c.ExpiresAt = time.Now().Add(time.Hour)
	c.MediaMicrousd = 5000
	s, err := store.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = stopTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("request with pending auth state")
		return nil, nil
	})
	var out, errout bytes.Buffer
	if err := syncCommand(context.Background(), nil, &out, &errout, s, &c, path, dbpath); err == nil {
		t.Fatal("pending marker ignored")
	}
	var n int
	if err := s.DB.QueryRow(`SELECT count(*) FROM sync_runs`).Scan(&n); err != nil || n != 0 {
		t.Fatal("sync started before auth validation")
	}
}
