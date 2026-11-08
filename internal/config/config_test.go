package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnvTokenDoesNotReuseOtherCredentials(t *testing.T) {
	c := Default()
	c.ClientID = "app"
	c.AccessToken = "old"
	c.RefreshToken = "old-refresh"
	c.UserID = "1"
	c.OwnedEligible = true
	c.ExpiresAt = time.Now().Add(-time.Hour)
	ApplyEnv(&c, func(k string) string {
		if k == "XSTASH_ACCESS_TOKEN" {
			return "new"
		}
		return ""
	})
	if c.AccessToken != "new" || c.RefreshToken != "" || c.UserID != "" || !c.ExpiresAt.IsZero() || c.OwnedEligible {
		t.Fatal("stale account credentials survived token override")
	}
}
func TestEnvClientChangeClearsOldAppSecret(t *testing.T) {
	c := Config{ClientID: "old", ClientSecret: "secret", AccessToken: "token", RefreshToken: "refresh", UserID: "1", OwnedEligible: true}
	ApplyEnv(&c, func(k string) string {
		if k == "XSTASH_CLIENT_ID" {
			return "new"
		}
		return ""
	})
	if c.ClientSecret != "" || c.AccessToken != "" || c.RefreshToken != "" || c.UserID != "" || c.OwnedEligible {
		t.Fatal("credentials from old app survived")
	}
}
func TestRefreshOverrideDoesNotUseOldAccessToken(t *testing.T) {
	c := Config{AccessToken: "old", RefreshToken: "old-refresh", UserID: "1"}
	ApplyEnv(&c, func(k string) string {
		if k == "XSTASH_REFRESH_TOKEN" {
			return "new-refresh"
		}
		return ""
	})
	if c.AccessToken != "" || c.UserID != "" || c.RefreshToken != "new-refresh" {
		t.Fatal("old access token survived refresh override")
	}
}

func TestLegacyBudgetIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"budget_microusd":2000000,"cycle_day":12,"post_microusd":5000}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.LookupBatchSize != 5 || c.PostMicrousd != 5000 {
		t.Fatalf("legacy config lost defaults: %+v", c)
	}
}
