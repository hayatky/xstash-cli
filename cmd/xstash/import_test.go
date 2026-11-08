package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/lock"
	"github.com/hayatky/xstash-cli/internal/xapi"
)

func TestImportArgumentsDoNotEchoSensitiveMistakes(t *testing.T) {
	for _, args := range [][]string{
		{"--access-token=synthetic-secret"},
		{"synthetic-secret"},
		{"--replace=synthetic-secret"},
		{"--expires-at=synthetic-secret"},
		{"--scopes=synthetic-secret"},
	} {
		t.Setenv("XSTASH_HOME", t.TempDir())
		var out, errout bytes.Buffer
		err := run(context.Background(), append([]string{"config", "import"}, args...), &out, &errout)
		if err == nil {
			t.Fatal("invalid arguments accepted")
		}
		if strings.Contains(out.String()+errout.String()+err.Error(), "synthetic-secret") {
			t.Fatal("secret echoed")
		}
	}
}
func TestImportUsesConfigLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XSTASH_HOME", home)
	guard, err := lock.Acquire(filepath.Join(home, "config.json.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	var out, errout bytes.Buffer
	err = run(context.Background(), []string{"config", "import", "--client-id", "synthetic-public", "--scopes", xapi.RequiredScope}, &out, &errout)
	if err == nil || !strings.Contains(err.Error(), "another sync") {
		t.Fatal("config lock not enforced")
	}
	if _, err = os.Stat(filepath.Join(home, "config.json")); !os.IsNotExist(err) {
		t.Fatal("changed config while locked")
	}
}
func TestShowImportedMetadataNeverTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XSTASH_HOME", home)
	path := filepath.Join(home, "config.json")
	c := config.Default()
	c.ClientID = "synthetic-public"
	c.AccessToken = "synthetic-access"
	c.RefreshToken = "synthetic-refresh"
	c.TokenType = "Bearer"
	c.Scope = xapi.RequiredScope
	c.ScopeSource = "user_selected"
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if err := run(context.Background(), []string{"config", "show"}, &out, &errout); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), c.AccessToken) || strings.Contains(out.String(), c.RefreshToken) || !strings.Contains(out.String(), "user_selected") {
		t.Fatal("unsafe show output")
	}
}
