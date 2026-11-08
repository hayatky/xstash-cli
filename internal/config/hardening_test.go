package config

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSavePreservesMetadataAndPrivateAtomicReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "config.json")
	c := Default()
	c.AccessToken = "synthetic-old"
	c.Scope = "tweet.read users.read bookmark.read like.read offline.access"
	c.ScopeSource = "response"
	c.TokenType = "Bearer"
	c.RefreshState = "pending"
	c.RefreshStartedAt = time.Now().UTC()
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c.AccessToken = "synthetic-new"
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got != c {
		t.Fatal("metadata lost")
	}
	oldBytes := make([]byte, len(before))
	if _, err := old.ReadAt(oldBytes, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, oldBytes) {
		t.Fatal("old inode was modified in place")
	}
	if runtime.GOOS != "windows" {
		for _, tt := range []struct {
			path string
			mode os.FileMode
		}{{path, 0600}, {filepath.Dir(path), 0700}} {
			info, err := os.Stat(tt.path)
			if err != nil || info.Mode().Perm() != tt.mode {
				t.Fatal("unsafe permissions")
			}
		}
	}
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*"))
	if err != nil || len(temps) != 0 {
		t.Fatal("temporary credentials left behind")
	}
}
