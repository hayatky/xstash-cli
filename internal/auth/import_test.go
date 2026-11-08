package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/xapi"
)

func importOptions() ImportOptions {
	return ImportOptions{ClientID: "synthetic-public", Scope: xapi.RequiredScope}
}
func tokenPrompt(tokens ...string) func(context.Context, string) ([]byte, error) {
	i := 0
	return func(context.Context, string) ([]byte, error) {
		if i >= len(tokens) {
			return nil, errors.New("synthetic-secret-error")
		}
		v := []byte(tokens[i])
		i++
		return v, nil
	}
}
func TestImportUnknownExpiryAndProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := config.Default()
	c.UserID = "old-account"
	c.OwnedEligible = true
	c.PostMicrousd = 1234
	start := time.Now().UTC()
	if err := Import(context.Background(), &c, path, importOptions(), tokenPrompt("synthetic-access", "synthetic-refresh")); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "synthetic-access" || got.RefreshToken != "synthetic-refresh" || got.ScopeSource != "user_selected" || got.TokenType != "Bearer" {
		t.Fatal("wrong imported metadata")
	}
	if got.ExpiresAt.Before(start) || got.ExpiresAt.After(time.Now()) || got.ExpiresAt.IsZero() {
		t.Fatal("unknown expiry was not conservatively set")
	}
	if got.UserID != "" || got.OwnedEligible || got.PostMicrousd != 1234 || got.ClientSecret != "" {
		t.Fatal("account flags or prices not preserved/reset correctly")
	}
	if err := Ready(got); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0600 {
		t.Fatal("config is not private")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*"))
	if len(files) != 0 {
		t.Fatal("temporary files remain")
	}
}
func TestImportExpiryIsAbsolute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := config.Default()
	opts := importOptions()
	opts.ExpiresAt = time.Date(2020, 1, 2, 3, 4, 5, 0, time.FixedZone("test", 3600))
	if err := Import(context.Background(), &c, path, opts, tokenPrompt("synthetic-access", "synthetic-refresh")); err != nil {
		t.Fatal(err)
	}
	if !c.ExpiresAt.Equal(opts.ExpiresAt) || c.ExpiresAt.Location() != time.UTC {
		t.Fatal("expiry was renewed instead of preserving absolute expiry")
	}
}
func TestImportRefusesUnsafeMetadataBeforePrompt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config.Config, *ImportOptions)
	}{
		{"missing-id", func(c *config.Config, o *ImportOptions) { o.ClientID = "" }},
		{"secret-client", func(c *config.Config, o *ImportOptions) { c.ClientSecret = "synthetic-secret" }},
		{"missing-scopes", func(c *config.Config, o *ImportOptions) { o.Scope = "" }},
		{"extra-scope", func(c *config.Config, o *ImportOptions) { o.Scope += " tweet.write" }},
		{"missing-scope", func(c *config.Config, o *ImportOptions) { o.Scope = "tweet.read" }},
		{"existing-access", func(c *config.Config, o *ImportOptions) { c.AccessToken = "old" }},
		{"existing-refresh", func(c *config.Config, o *ImportOptions) { c.RefreshToken = "old" }},
		{"pending", func(c *config.Config, o *ImportOptions) { c.RefreshState = "pending" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Default()
			o := importOptions()
			tc.mutate(&c, &o)
			old := c
			err := Import(context.Background(), &c, filepath.Join(t.TempDir(), "config.json"), o, func(context.Context, string) ([]byte, error) {
				t.Fatal("prompted before validating metadata")
				return nil, nil
			})
			if err == nil || !reflect.DeepEqual(c, old) {
				t.Fatal("unsafe import accepted or config mutated")
			}
		})
	}
}
func TestImportReplacementRequiresFreshToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := syntheticConfig()
	c.RefreshState = "pending"
	c.RefreshStartedAt = time.Now()
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	opts := importOptions()
	opts.Replace = true
	err := Import(context.Background(), &c, path, opts, tokenPrompt("synthetic-new-access", c.RefreshToken))
	if err == nil {
		t.Fatal("replayed refresh token accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("pending marker changed")
	}
	if err := Import(context.Background(), &c, path, opts, tokenPrompt("synthetic-new-access", "synthetic-new-refresh")); err != nil {
		t.Fatal(err)
	}
	if c.RefreshState != "" || !c.RefreshStartedAt.IsZero() {
		t.Fatal("fresh grant did not clear old marker")
	}
}
func TestImportCancelledAndInvalidNeverWrite(t *testing.T) {
	for _, tokens := range [][]string{{}, {"synthetic-access"}, {""}, {"with whitespace"}, {"synthetic-access", ""}, {"synthetic-access", "bad\nvalue"}, {strings.Repeat("x", 16*1024+1)}} {
		c := syntheticConfig()
		path := filepath.Join(t.TempDir(), "config.json")
		if e := config.Save(path, c); e != nil {
			t.Fatal(e)
		}
		before, _ := os.ReadFile(path)
		opts := importOptions()
		opts.Replace = true
		err := Import(context.Background(), &c, path, opts, tokenPrompt(tokens...))
		if err == nil {
			t.Fatal("invalid import accepted")
		}
		if strings.Contains(err.Error(), "synthetic-secret-error") {
			t.Fatal("input error leaked")
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("failed import changed credentials")
		}
	}
}
func TestImportCancellationAfterInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := config.Default()
	path := filepath.Join(t.TempDir(), "config.json")
	calls := 0
	err := Import(ctx, &c, path, importOptions(), func(context.Context, string) ([]byte, error) {
		calls++
		if calls == 2 {
			cancel()
		}
		return []byte("synthetic-token"), nil
	})
	if err == nil {
		t.Fatal("cancelled import accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cancelled import wrote config")
	}
}
func TestImportSaveFailureDoesNotMutateMemory(t *testing.T) {
	c := config.Default()
	old := c
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := Import(context.Background(), &c, filepath.Join(parent, "config.json"), importOptions(), func(context.Context, string) ([]byte, error) {
		calls++
		if calls == 2 {
			if err := os.Remove(parent); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(parent, []byte("synthetic-obstruction"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return []byte("synthetic-token"), nil
	})
	if err == nil || !reflect.DeepEqual(c, old) || !strings.Contains(err.Error(), "may already have completed") {
		t.Fatal("failed save mutated config or did not report uncertain save")
	}
}
func TestImportRejectsSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skip(err)
	}
	if err := CheckImport(config.Default(), path, importOptions()); err == nil {
		t.Fatal("accepted symlink")
	}
}
