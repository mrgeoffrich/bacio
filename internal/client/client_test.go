package client_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mrgeoffrich/bacio/internal/client"
)

func TestOpenLocal(t *testing.T) {
	dir := t.TempDir()
	c, err := client.Open(context.Background(), client.Options{
		DBPath: filepath.Join(dir, "db.sqlite"),
		Actor:  "tester",
	})
	if err != nil {
		t.Fatalf("Open(local): %v", err)
	}
	defer c.Close()
	if got := c.Mode(); got != client.ModeLocal {
		t.Fatalf("Mode = %q, want %q", got, client.ModeLocal)
	}
}

func TestOpenRemote(t *testing.T) {
	c, err := client.Open(context.Background(), client.Options{
		Remote: "http://127.0.0.1:5320",
		Actor:  "tester",
	})
	if err != nil {
		t.Fatalf("Open(remote): %v", err)
	}
	defer c.Close()
	if got := c.Mode(); got != client.ModeRemote {
		t.Fatalf("Mode = %q, want %q", got, client.ModeRemote)
	}
}

func TestOpenRemoteRejectsBadURL(t *testing.T) {
	if _, err := client.Open(context.Background(), client.Options{Remote: "not-a-url"}); err == nil {
		t.Fatalf("expected error on missing scheme/host")
	}
}
