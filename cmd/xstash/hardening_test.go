package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/store"
)

func isolatedHardeningHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XSTASH_HOME", home)
	for _, key := range []string{"XSTASH_ACCESS_TOKEN", "XSTASH_REFRESH_TOKEN", "XSTASH_CLIENT_ID", "XSTASH_CLIENT_SECRET"} {
		t.Setenv(key, "")
	}
	return filepath.Join(home, "config.json"), filepath.Join(home, "xstash-go.sqlite")
}

func TestOrdinaryCommandsDoNotCreateMissingDatabase(t *testing.T) {
	for _, command := range []string{"stats", "pending", "export", "complete", "sync", "unknown", "db"} {
		t.Run(command, func(t *testing.T) {
			_, dbPath := isolatedHardeningHome(t)
			args := []string{command}
			if command == "db" {
				args = append(args, "check")
			}
			var stdout, stderr bytes.Buffer
			if err := run(context.Background(), args, &stdout, &stderr); err == nil {
				t.Fatal("missing database accepted")
			}
			if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
				t.Fatalf("database created: %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("unexpected success output: %s", stdout.String())
			}
		})
	}
}

func TestExplicitDatabaseInitAndCheck(t *testing.T) {
	_, dbPath := isolatedHardeningHome(t)
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"db", "init"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := run(context.Background(), []string{"db", "check"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report["database_exists"] != true || report["schema_version"] != float64(1) || report["quick_check"] != "ok" {
		t.Fatalf("incorrect check report: %v", report)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("db check changed database content")
	}
}

func TestDatabaseCheckRejectsInvalidFileWithoutMutation(t *testing.T) {
	for _, content := range []string{"", "synthetic invalid database"} {
		t.Run(content, func(t *testing.T) {
			_, dbPath := isolatedHardeningHome(t)
			if err := os.WriteFile(dbPath, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			if err := run(context.Background(), []string{"db", "check"}, &stdout, &stderr); err == nil {
				t.Fatal("invalid database accepted")
			}
			after, err := os.ReadFile(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != content {
				t.Fatal("db check modified invalid database")
			}
		})
	}
}

func syntheticAuthConfig() config.Config {
	c := config.Default()
	c.ClientID = "synthetic-client"
	c.ClientSecret = "synthetic-secret-never-display"
	c.UserID = "synthetic-user"
	c.AccessToken = "synthetic-access-never-display"
	c.RefreshToken = "synthetic-refresh-never-display"
	c.TokenType = "bearer"
	c.Scope = "tweet.read users.read bookmark.read like.read offline.access"
	c.ScopeSource = "token_response"
	c.RefreshState = "in_progress"
	c.RefreshStartedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.ExpiresAt = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	return c
}

func TestConfigSetRejectsTokenEnvironmentWithoutMutation(t *testing.T) {
	for _, key := range []string{"XSTASH_ACCESS_TOKEN", "XSTASH_REFRESH_TOKEN"} {
		t.Run(key, func(t *testing.T) {
			path, _ := isolatedHardeningHome(t)
			if err := config.Save(path, syntheticAuthConfig()); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(key, "synthetic-env-token")
			var stdout, stderr bytes.Buffer
			if err := run(context.Background(), []string{"config", "set", "--lookup-batch-size", "7"}, &stdout, &stderr); err == nil {
				t.Fatal("token override accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("config changed after rejected token override")
			}
		})
	}
}

func TestConfigShowReportsMetadataWithoutSecrets(t *testing.T) {
	path, _ := isolatedHardeningHome(t)
	c := syntheticAuthConfig()
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"config", "show"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{c.ClientSecret, c.AccessToken, c.RefreshToken} {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Fatal("config show disclosed synthetic secret")
		}
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"token_type": c.TokenType, "scope": c.Scope, "scope_source": c.ScopeSource, "refresh_state": c.RefreshState, "refresh_started_at": c.RefreshStartedAt.Format(time.RFC3339), "access_token_set": true, "refresh_token_set": true, "auth_metadata_valid": false} {
		if report[key] != want {
			t.Errorf("%s=%v want %v", key, report[key], want)
		}
	}
}

func TestConfigSetPreservesAuthenticationState(t *testing.T) {
	path, _ := isolatedHardeningHome(t)
	before := syntheticAuthConfig()
	if err := config.Save(path, before); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"config", "set", "--lookup-batch-size", "7"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	after, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	before.LookupBatchSize = 7
	if !reflect.DeepEqual(after, before) {
		t.Fatal("config set changed authentication metadata or other unrelated settings")
	}
}

func TestOnlyExplicitDBCheckScansForeignKeyData(t *testing.T) {
	t.Setenv("XSTASH_HOME", t.TempDir())
	_, path, err := config.Paths()
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`PRAGMA foreign_keys=OFF; INSERT INTO processing VALUES('missing-post','analysis','synthetic-time')`); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out, errout bytes.Buffer
	if err = run(context.Background(), []string{"stats"}, &out, &errout); err != nil {
		t.Fatalf("ordinary command performed full data check: %v", err)
	}
	if err = run(context.Background(), []string{"db", "check"}, &out, &errout); err == nil {
		t.Fatal("explicit integrity check accepted foreign-key violation")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("checks modified data")
	}
}
