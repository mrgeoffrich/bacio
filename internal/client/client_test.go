package client

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenLocal(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(context.Background(), Options{
		DBPath: filepath.Join(dir, "db.sqlite"),
		Actor:  "tester",
	})
	if err != nil {
		t.Fatalf("Open(local): %v", err)
	}
	defer c.Close()
	if got := c.Mode(); got != ModeLocal {
		t.Fatalf("Mode = %q, want %q", got, ModeLocal)
	}
}

func TestOpenRemote(t *testing.T) {
	c, err := Open(context.Background(), Options{
		Remote: "http://127.0.0.1:5320",
		Actor:  "tester",
	})
	if err != nil {
		t.Fatalf("Open(remote): %v", err)
	}
	defer c.Close()
	if got := c.Mode(); got != ModeRemote {
		t.Fatalf("Mode = %q, want %q", got, ModeRemote)
	}
}

func TestOpenRemoteRejectsBadURL(t *testing.T) {
	if _, err := Open(context.Background(), Options{Remote: "not-a-url"}); err == nil {
		t.Fatalf("expected error on missing scheme/host")
	}
}
