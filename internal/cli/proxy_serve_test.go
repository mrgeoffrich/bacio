package cli

import (
	"bytes"
	"testing"
)

// TestProxyServeCmd_FlagsAndArgs pins the `bacio proxy serve` shim's
// surface: it carries the --addr / --port overrides (the default-addr
// resolution is exercised via the wtenv resolver tests) and refuses
// positional noise (cobra.NoArgs). The server's forwarding / healthz /
// capture behaviour is covered end to end in internal/api's
// proxy_server_test.go through api.NewProxyServer.
func TestProxyServeCmd_FlagsAndArgs(t *testing.T) {
	cmd := newProxyServeCmd()
	for _, name := range []string{"addr", "port"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("proxy serve missing --%s flag", name)
		}
	}

	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"unexpected"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error from positional arg, got nil (cobra.NoArgs not wired)")
	}
}

// TestProxyServeCmd_RegisteredUnderProxy guards the wiring: `serve` must be
// reachable as a subcommand of `bacio proxy` so the dispatch / install path
// can invoke it.
func TestProxyServeCmd_RegisteredUnderProxy(t *testing.T) {
	parent := newProxyCmd()
	sub, _, err := parent.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("find proxy serve: %v", err)
	}
	if sub == nil || sub.Name() != "serve" {
		t.Fatalf("proxy serve not registered under `bacio proxy`; got %v", sub)
	}
}
