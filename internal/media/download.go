package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/hayatky/xstash-cli/internal/store"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Media struct {
	Key      string `json:"media_key"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Preview  string `json:"preview_image_url"`
	Variants []struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
		BitRate     int    `json:"bit_rate"`
	} `json:"variants"`
}

func allowed(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "pbs.twimg.com" || h == "video.twimg.com"
}
func Download(ctx context.Context, s *store.Store, dir string) (int, error) {
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || !allowed(r.URL.String()) {
			return fmt.Errorf("media redirect rejected")
		}
		return nil
	}}
	return download(ctx, s, dir, client)
}
func download(ctx context.Context, s *store.Store, dir string, client *http.Client) (int, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT media_key,raw_json FROM media WHERE local_path IS NULL ORDER BY media_key`)
	if e != nil {
		return 0, e
	}
	var entries []Media
	for rows.Next() {
		var key, raw string
		if e = rows.Scan(&key, &raw); e != nil {
			rows.Close()
			return 0, e
		}
		var m Media
		if e = json.Unmarshal([]byte(raw), &m); e != nil {
			rows.Close()
			return 0, e
		}
		m.Key = key
		entries = append(entries, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return 0, e
	}
	n := 0
	var failed int
	for _, m := range entries {
		raw := m.URL
		ext := ".jpg"
		if m.Type == "video" || m.Type == "animated_gif" {
			sort.Slice(m.Variants, func(i, j int) bool { return m.Variants[i].BitRate > m.Variants[j].BitRate })
			raw = ""
			for _, v := range m.Variants {
				if v.ContentType == "video/mp4" {
					raw = v.URL
					ext = ".mp4"
					break
				}
			}
			if raw == "" {
				raw = m.Preview
				ext = ".jpg"
			}
		}
		if !allowed(raw) {
			failed++
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			failed++
			continue
		}
		resp, err := getWithRetry(ctx, client, req)
		if err != nil {
			failed++
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			failed++
			continue
		}
		nameHash := sha256.Sum256([]byte(m.Key))
		path := filepath.Join(dir, hex.EncodeToString(nameHash[:])+ext)
		temp, err := os.CreateTemp(dir, ".download-*")
		if err != nil {
			resp.Body.Close()
			return n, err
		}
		name := temp.Name()
		_, err = io.Copy(temp, io.LimitReader(resp.Body, 100*1024*1024+1))
		resp.Body.Close()
		stat, statErr := temp.Stat()
		closeErr := temp.Close()
		if err != nil || statErr != nil || closeErr != nil || stat.Size() > 100*1024*1024 {
			os.Remove(name)
			failed++
			continue
		}
		if err = os.Rename(name, path); err != nil {
			os.Remove(name)
			failed++
			continue
		}
		if _, err = s.DB.ExecContext(ctx, `UPDATE media SET local_path=? WHERE media_key=?`, path, m.Key); err != nil {
			return n, err
		}
		n++
	}
	if failed > 0 {
		return n, fmt.Errorf("%d media downloads failed or had unsupported URLs", failed)
	}
	return n, nil
}

func getWithRetry(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := client.Do(req.Clone(ctx))
		if err == nil && (resp.StatusCode != 429 && resp.StatusCode < 500 || attempt == 2) {
			return resp, nil
		}
		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil && attempt == 2 {
			return nil, err
		}
		delay := time.Duration(1<<attempt) * time.Second
		if resp != nil {
			if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n >= 0 {
				delay = time.Duration(n) * time.Second
			}
			resp.Body.Close()
		}
		if delay > 60*time.Second {
			return nil, fmt.Errorf("media retry delay exceeds 60 seconds")
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("media retries exhausted")
}
