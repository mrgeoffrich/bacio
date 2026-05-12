// Command bacio-wasm is the browser build of bacio's TUI. Compiled with
// GOOS=js GOARCH=wasm, it speaks to an xterm.js terminal via a small
// syscall/js bridge: writes go to a JS callback registered as
// `bacioOnStdout`, key events arrive through `bacioWriteStdin`, and the
// program signals readiness by calling `bacioReady`.
//
// Phase 1 (this file): proves the pipe by streaming a static banner +
// a slow heartbeat. The full TUI lights up in later phases.
package main

import (
	"fmt"
	"syscall/js"
	"time"
)

func main() {
	out := newJSOut("bacioOnStdout")

	// Hardcoded banner. ANSI colour codes are passed straight through to
	// xterm.js which renders them natively — no extra setup required.
	banner := "" +
		"\x1b[38;5;181m" + // pale gold
		"  ___  ___   ___ ___ ___\n" +
		" | _ )/ _ \\ / __|_ _/ _ \\\n" +
		" | _ \\ (_) | (__ | | (_) |\n" +
		" |___/\\___/ \\___|___\\___/\n" +
		"\x1b[0m\n" +
		"\x1b[2m bacio-wasm · phase 1 · hello from a Go binary running in your browser\x1b[0m\n\n"
	fmt.Fprint(out, banner)

	// Tell the host we're up so it can dismiss any loading spinner.
	if cb := js.Global().Get("bacioReady"); cb.Truthy() {
		cb.Invoke()
	}

	// Heartbeat so it's obvious we're alive (and that the bridge keeps
	// flushing). Removed once we wire up Bubble Tea in phase 3.
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for t := range tick.C {
		fmt.Fprintf(out, "\x1b[2m  · tick %s\x1b[0m\n", t.Format("15:04:05"))
	}
}
