package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestNoninteractivePendingAndComplete(t *testing.T) {
	t.Setenv("XSTASH_HOME", t.TempDir())
	_, path, e := config.Paths()
	if e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SavePage(context.Background(), "like", []store.Post{{ID: "1", Text: "hello"}}, store.Now())
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	var out, errout bytes.Buffer
	if e = run(context.Background(), []string{"pending", "--purpose", "analysis"}, &out, &errout); e != nil {
		t.Fatal(e)
	}
	var v struct {
		Items []any `json:"items"`
	}
	if e = json.Unmarshal(out.Bytes(), &v); e != nil || len(v.Items) != 1 {
		t.Fatalf("pending: %v", e)
	}
	out.Reset()
	if e = run(context.Background(), []string{"complete", "--purpose", "analysis", "--id", "1"}, &out, &errout); e != nil {
		t.Fatal(e)
	}
	out.Reset()
	if e = run(context.Background(), []string{"pending", "--purpose", "analysis"}, &out, &errout); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(out.Bytes(), &v); e != nil || len(v.Items) != 0 {
		t.Fatal("not completed")
	}
}

func TestCurrentMonthSeparatesEstimateAndUncertainty(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	id, e := s.Reserve(ctx, "r", now, 3000000)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RecordAttempt(ctx, id, "r", "/2/tweets", now, 200, []store.Read{{Type: "post", ID: "1", Price: 5000}}, true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reserve(ctx, "r", now, 40000); e != nil {
		t.Fatal(e)
	}
	old, e := s.Reserve(ctx, "r", now.AddDate(0, -1, 0), 70000)
	if e != nil {
		t.Fatal(e)
	}
	_ = old
	v, e := currentMonth(ctx, s, now)
	if e != nil {
		t.Fatal(e)
	}
	if v["estimated_usd"] != 0.005 || v["uncertain_max_usd"] != 0.04 || v["month_utc"] != "2026-11" {
		t.Fatalf("month: %#v", v)
	}
}
