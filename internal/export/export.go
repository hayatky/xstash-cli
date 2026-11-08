package export

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/hayatky/xstash-cli/internal/store"
	"io"
	"os"
	"sort"
	"strings"
)

type Relation struct {
	Kind             string  `json:"kind"`
	DiscoveredAt     string  `json:"discovered_at"`
	LastSeenAt       string  `json:"last_seen_at"`
	Present          bool    `json:"present"`
	OperatedAt       *string `json:"operated_at"`
	OperatedAtSource string  `json:"operated_at_source"`
}
type Media struct {
	Data      json.RawMessage `json:"data"`
	LocalPath *string         `json:"local_path"`
}
type Item struct {
	Post       json.RawMessage   `json:"post"`
	Author     json.RawMessage   `json:"author,omitempty"`
	Media      []Media           `json:"media"`
	Relations  []Relation        `json:"relations"`
	References []store.Reference `json:"references"`
	FetchedAt  string            `json:"fetched_at"`
}
type Dataset struct {
	SchemaVersion string `json:"schema_version"`
	Items         []Item `json:"items"`
}
type Options struct {
	Kind, Since, Until string
	IncludeReferenced  bool
}

func Collect(ctx context.Context, s *store.Store, o Options) (Dataset, error) {
	d := Dataset{SchemaVersion: "2.0.0", Items: []Item{}}
	if o.Kind != "" && o.Kind != "bookmark" && o.Kind != "like" {
		return d, fmt.Errorf("invalid kind")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT p.id FROM posts p WHERE EXISTS (SELECT 1 FROM relations r WHERE r.post_id=p.id AND (?='' OR r.kind=?) AND (?='' OR substr(r.discovered_at,1,10)>=?) AND (?='' OR substr(r.discovered_at,1,10)<=?)) ORDER BY p.id`, o.Kind, o.Kind, o.Since, o.Since, o.Until, o.Until)
	if err != nil {
		return d, err
	}
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return d, err
		}
		ids[id] = true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return d, err
	}
	rows.Close()
	if o.IncludeReferenced {
		front := make([]string, 0, len(ids))
		for id := range ids {
			front = append(front, id)
		}
		for depth := 0; depth < 3 && len(front) > 0; depth++ {
			var next []string
			for _, id := range front {
				r, e := s.DB.QueryContext(ctx, `SELECT refs.target_id FROM refs JOIN posts ON posts.id=refs.target_id WHERE refs.source_id=? AND refs.kind='quoted' ORDER BY refs.target_id`, id)
				if e != nil {
					return d, e
				}
				for r.Next() {
					var target string
					if e = r.Scan(&target); e != nil {
						r.Close()
						return d, e
					}
					if !ids[target] {
						ids[target] = true
						next = append(next, target)
					}
				}
				e = r.Err()
				r.Close()
				if e != nil {
					return d, e
				}
			}
			front = next
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		item := Item{Media: []Media{}, Relations: []Relation{}, References: []store.Reference{}}
		var raw, author string
		if err = s.DB.QueryRowContext(ctx, `SELECT raw_json,author_id,fetched_at FROM posts WHERE id=?`, id).Scan(&raw, &author, &item.FetchedAt); err != nil {
			return d, err
		}
		item.Post = json.RawMessage(raw)
		var user string
		if err = s.DB.QueryRowContext(ctx, `SELECT raw_json FROM users WHERE id=?`, author).Scan(&user); err == nil {
			item.Author = json.RawMessage(user)
		}
		r, e := s.DB.QueryContext(ctx, `SELECT kind,discovered_at,last_seen_at,present FROM relations WHERE post_id=? ORDER BY kind`, id)
		if e != nil {
			return d, e
		}
		for r.Next() {
			var rel Relation
			var present int
			if e = r.Scan(&rel.Kind, &rel.DiscoveredAt, &rel.LastSeenAt, &present); e != nil {
				r.Close()
				return d, e
			}
			rel.Present = present != 0
			rel.OperatedAtSource = "not_provided_by_x_api"
			item.Relations = append(item.Relations, rel)
		}
		e = r.Err()
		r.Close()
		if e != nil {
			return d, e
		}
		r, e = s.DB.QueryContext(ctx, `SELECT target_id,kind FROM refs WHERE source_id=? ORDER BY kind,target_id`, id)
		if e != nil {
			return d, e
		}
		for r.Next() {
			var ref store.Reference
			if e = r.Scan(&ref.ID, &ref.Type); e != nil {
				r.Close()
				return d, e
			}
			item.References = append(item.References, ref)
		}
		e = r.Err()
		r.Close()
		if e != nil {
			return d, e
		}
		r, e = s.DB.QueryContext(ctx, `SELECT m.raw_json,m.local_path FROM media m JOIN post_media pm ON pm.media_key=m.media_key WHERE pm.post_id=? ORDER BY m.media_key`, id)
		if e != nil {
			return d, e
		}
		for r.Next() {
			var raw string
			var path sql.NullString
			if e = r.Scan(&raw, &path); e != nil {
				r.Close()
				return d, e
			}
			m := Media{Data: json.RawMessage(raw)}
			if path.Valid {
				if info, err := os.Stat(path.String); err == nil && info.Mode().IsRegular() {
					p := path.String
					m.LocalPath = &p
				}
			}
			item.Media = append(item.Media, m)
		}
		e = r.Err()
		r.Close()
		if e != nil {
			return d, e
		}
		d.Items = append(d.Items, item)
	}
	return d, nil
}
func Write(w io.Writer, format string, d Dataset) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	case "csv":
		c := csv.NewWriter(w)
		if e := c.Write([]string{"post_id", "text", "author_id", "created_at", "kinds", "fetched_at", "operated_at"}); e != nil {
			return e
		}
		for _, item := range d.Items {
			var p store.Post
			if e := json.Unmarshal(item.Post, &p); e != nil {
				return e
			}
			var kinds []string
			for _, r := range item.Relations {
				kinds = append(kinds, r.Kind)
			}
			if e := c.Write([]string{p.ID, p.Text, p.AuthorID, p.CreatedAt, strings.Join(kinds, "|"), item.FetchedAt, ""}); e != nil {
				return e
			}
		}
		c.Flush()
		return c.Error()
	case "markdown":
		lookup := map[string]Item{}
		for _, item := range d.Items {
			var p store.Post
			if e := json.Unmarshal(item.Post, &p); e != nil {
				return e
			}
			lookup[p.ID] = item
		}
		for _, item := range d.Items {
			var p store.Post
			if e := json.Unmarshal(item.Post, &p); e != nil {
				return e
			}
			if _, e := fmt.Fprintf(w, "## %s\n\n%s\n\n", p.ID, p.Text); e != nil {
				return e
			}
			for _, r := range item.References {
				if _, e := fmt.Fprintf(w, "- %s: https://x.com/i/status/%s\n", r.Type, r.ID); e != nil {
					return e
				}
				if r.Type == "quoted" {
					if e := writeQuote(w, r.ID, lookup, 1, map[string]bool{p.ID: true}); e != nil {
						return e
					}
				}
			}
			for _, media := range item.Media {
				var m struct {
					URL     string `json:"url"`
					Preview string `json:"preview_image_url"`
				}
				_ = json.Unmarshal(media.Data, &m)
				if media.LocalPath != nil {
					m.URL = *media.LocalPath
				}
				if m.URL == "" {
					m.URL = m.Preview
				}
				if m.URL != "" {
					if _, e := fmt.Fprintf(w, "- Media: %s\n", m.URL); e != nil {
						return e
					}
				}
			}
			if _, e := fmt.Fprintln(w); e != nil {
				return e
			}
		}
		return nil
	default:
		return fmt.Errorf("format must be json, markdown, or csv")
	}
}

func writeQuote(w io.Writer, id string, items map[string]Item, depth int, seen map[string]bool) error {
	if depth > 3 || seen[id] {
		return nil
	}
	item, ok := items[id]
	if !ok {
		return nil
	}
	seen[id] = true
	defer delete(seen, id)
	var p store.Post
	if err := json.Unmarshal(item.Post, &p); err != nil {
		return err
	}
	prefix := strings.Repeat("> ", depth)
	for _, line := range strings.Split(p.Text, "\n") {
		if _, err := fmt.Fprintln(w, prefix+line); err != nil {
			return err
		}
	}
	for _, ref := range item.References {
		if ref.Type == "quoted" {
			if err := writeQuote(w, ref.ID, items, depth+1, seen); err != nil {
				return err
			}
		}
	}
	return nil
}
