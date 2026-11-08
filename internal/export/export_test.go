package export

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/hayatky/xstash-cli/internal/store"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatsAndReferences(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := store.Post{ID: "1", Text: "hello, world", References: []store.Reference{{ID: "2", Type: "quoted"}}}
	if _, e = s.SavePage(ctx, "bookmark", []store.Post{p}, "2026-01-01T00:00:00.000000000Z"); e != nil {
		t.Fatal(e)
	}
	if e = s.ResolveQuote(ctx, "2", &store.Post{ID: "2", Text: "quoted"}, "2026-01-01T00:00:01.000000000Z"); e != nil {
		t.Fatal(e)
	}
	d, e := Collect(ctx, s, Options{IncludeReferenced: true})
	if e != nil || len(d.Items) != 2 {
		t.Fatalf("dataset %v %v", d, e)
	}
	for _, format := range []string{"json", "markdown", "csv"} {
		var b bytes.Buffer
		if e = Write(&b, format, d); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(b.String(), "hello, world") {
			t.Fatalf("%s missing text", format)
		}
		if format == "json" {
			var v map[string]any
			if e = json.Unmarshal(b.Bytes(), &v); e != nil || v["schema_version"] != "2.0.0" {
				t.Fatalf("invalid JSON %v", e)
			}
		}
	}
	d, e = Collect(ctx, s, Options{Since: "2026-02-01"})
	if e != nil || len(d.Items) != 0 {
		t.Fatal("date filter")
	}
}
