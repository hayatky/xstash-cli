package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/store"
	"github.com/hayatky/xstash-cli/internal/xapi"
)

func TestDefaultSyncCostPolicyEndToEnd(t *testing.T) {
	dir := t.TempDir()
	dbpath := filepath.Join(dir, "synthetic.db")
	s, err := store.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := config.Default()
	c.AccessToken = "synthetic"
	c.RefreshToken = "synthetic-refresh"
	c.TokenType = "Bearer"
	c.Scope = xapi.RequiredScope
	c.UserID = "123"
	c.ExpiresAt = time.Now().Add(time.Hour)
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	pages, lookups := 0, 0
	http.DefaultTransport = stopTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("expansions") != "" {
			t.Fatal("default sync requested metadata")
		}
		var data []string
		next := ""
		if r.URL.Path == "/2/tweets" {
			ids := strings.Split(r.URL.Query().Get("ids"), ",")
			if len(ids) > 5 {
				t.Fatal("lookup batch exceeded default")
			}
			lookups += len(ids)
			for _, id := range ids {
				data = append(data, fmt.Sprintf(`{"id":%q,"author_id":"a","referenced_tweets":[{"id":"child%s","type":"quoted"}]}`, id, id))
			}
		} else {
			pages++
			if r.URL.Query().Get("max_results") != "5" {
				t.Fatal("default page size changed")
			}
			start, _ := strconv.Atoi(r.URL.Query().Get("pagination_token"))
			for i := start + 1; i <= start+5; i++ {
				id := i
				ref := ""
				if strings.HasSuffix(r.URL.Path, "/liked_tweets") {
					id += 5000
				} else if !strings.HasSuffix(r.URL.Path, "/bookmarks") {
					t.Fatalf("unexpected endpoint %s", r.URL.Path)
				} else if i <= 50 {
					ref = fmt.Sprintf(`,"referenced_tweets":[{"id":"%d","type":"quoted"}]`, i+1000)
				}
				data = append(data, fmt.Sprintf(`{"id":"%d","author_id":"a"%s}`, id, ref))
			}
			next = fmt.Sprintf(`,"next_token":"%d"`, start+5)
		}
		body := fmt.Sprintf(`{"data":[%s],"meta":{"result_count":%d%s}}`, strings.Join(data, ","), len(data), next)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	var out, errout bytes.Buffer
	args := []string{}
	if err = syncCommand(context.Background(), args, &out, &errout, s, &c, filepath.Join(dir, "config.json"), dbpath); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status  string `json:"status"`
		Results []struct {
			Retrieved int    `json:"retrieved_posts"`
			Complete  bool   `json:"complete"`
			History   bool   `json:"history_complete"`
			Reason    string `json:"reason"`
		} `json:"results"`
		Deferred        int   `json:"deferred_quotes"`
		PendingMetadata int   `json:"pending_metadata"`
		Estimated       int64 `json:"estimated_microusd"`
		Policy          struct {
			Depth    int  `json:"quote_depth"`
			Metadata bool `json:"metadata_requested"`
			Cap      bool `json:"monthly_spend_cap"`
		} `json:"policy"`
	}
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if pages != 30 || lookups != 50 || result.Status != "completed" || len(result.Results) != 2 || result.Results[0].Retrieved != 100 || result.Results[1].Retrieved != 50 || result.Deferred != 50 || result.PendingMetadata != 200 || result.Estimated != 1000000 {
		t.Fatalf("pages=%d lookups=%d result=%s", pages, lookups, out.String())
	}
	for _, v := range result.Results {
		if !v.Complete || v.History || v.Reason != "initial_limit" {
			t.Fatal("initial window misreported")
		}
	}
	if result.Policy.Depth != 1 || result.Policy.Metadata || result.Policy.Cap {
		t.Fatal("unexpected cost policy")
	}
	out.Reset()
	pages = 0
	lookups = 0
	if err = syncCommand(context.Background(), nil, &out, &errout, s, &c, filepath.Join(dir, "config.json"), dbpath); err != nil {
		t.Fatal(err)
	}
	if pages != 2 || lookups != 0 {
		t.Fatalf("repeat sync re-fetched history: pages=%d quotes=%d", pages, lookups)
	}
}

func TestInvalidCostOptionsAndUnknownExpansionPriceStopBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{{"--quote-depth", "4"}, {"--initial-likes", "-1"}, {"--max-pages", "-1"}, {"--max-quote-lookups", "-1"}, {"--max-metadata-lookups", "-1"}, {"--metadata"}, {"--media"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			c := config.Default()
			c.AccessToken = "synthetic"
			c.TokenType = "Bearer"
			c.Scope = xapi.RequiredScope
			c.UserID = "123"
			var out, errout bytes.Buffer
			// nil store is safe: each option must fail preflight, before any DB/API work.
			if err := syncCommand(context.Background(), args, &out, &errout, nil, &c, "unused", "unused"); err == nil {
				t.Fatal("invalid cost option accepted")
			}
		})
	}
}

func TestInitialIdentityEnablesOnlyConfirmedOwnedLists(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := config.Default()
	c.AccessToken, c.TokenType, c.Scope = "synthetic", "Bearer", xapi.RequiredScope
	c.ExpiresAt = time.Now().Add(time.Hour)
	c.OwnedEligible = true
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = stopTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"id":"1"}],"meta":{"result_count":1}}`
		if r.URL.Path == "/2/users/me" {
			body = `{"data":{"id":"123"}}`
		} else if r.URL.Path != "/2/users/123/bookmarks" {
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	var out, errout bytes.Buffer
	if err := syncCommand(context.Background(), []string{"--kind", "bookmark", "--quote-depth", "0"}, &out, &errout, s, &c, filepath.Join(dir, "config.json"), filepath.Join(dir, "db")); err != nil {
		t.Fatal(err)
	}
	var estimate int64
	if err := s.DB.QueryRow(`SELECT sum(estimated_microusd) FROM api_requests`).Scan(&estimate); err != nil || estimate != 11000 {
		t.Fatalf("estimate=%d err=%v", estimate, err)
	}
}
