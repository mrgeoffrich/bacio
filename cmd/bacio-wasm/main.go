//go:build js && wasm

// Command bacio-wasm is the browser build of bacio's TUI. Compiled with
// GOOS=js GOARCH=wasm, it speaks to an xterm.js terminal via a small
// syscall/js bridge:
//
//   window.bacioOnStdout(string)  — Go writes; called from Go.
//   window.bacioWriteStdin(string) — JS feeds Go keypresses; xterm.js
//                                    handles escape-sequence encoding.
//   window.bacioResize(cols, rows) — JS pushes a tea.WindowSizeMsg so
//                                    the layout reflows.
//   window.bacioReady()            — Go signals "I'm up" to JS.
//
// Phase 4 (this file): runs the real internal/tui Model against an
// in-memory store seeded with the gelato kanban. Read-only — there's
// no save loop, so even if Bubble Tea routes a write-intent keystroke
// through, the underlying store changes are scoped to this page load.
package main

import (
	"fmt"
	"syscall/js"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrgeoffrich/bacio/internal/store"
	"github.com/mrgeoffrich/bacio/internal/tui"
	"github.com/mrgeoffrich/bacio/internal/wasm"
)

func main() {
	out := newJSOut("bacioOnStdout")
	in := newJSIn() // exposed to JS as window.bacioWriteStdin

	s, err := store.OpenMemory()
	if err != nil {
		fmt.Fprintf(out, "\r\n\x1b[31mfailed to open in-memory store: %v\x1b[0m\r\n", err)
		return
	}
	repo, err := wasm.Seed(s, wasm.SeedOptions{})
	if err != nil {
		fmt.Fprintf(out, "\r\n\x1b[31mfailed to seed demo data: %v\x1b[0m\r\n", err)
		return
	}
	innerModel, err := tui.NewModel(s, repo, nil) // nil elector: WASM demo always acts as leader
	if err != nil {
		fmt.Fprintf(out, "\r\n\x1b[31mfailed to build TUI model: %v\x1b[0m\r\n", err)
		return
	}

	// readyOnce fires window.bacioReady() from Init — that runs *after*
	// the program's event loop is draining p.msgs, which is the only
	// safe time to let JS push messages back into Send (the channel is
	// unbuffered).
	p := tea.NewProgram(
		&readyOnce{Model: innerModel},
		tea.WithInput(in),
		tea.WithOutput(out),
		tea.WithoutSignalHandler(),
		tea.WithoutSignals(),
		tea.WithoutCatchPanics(),
		// xterm.js renders inline, no alt-screen.
	)

	js.Global().Set("bacioResize", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 2 {
			return nil
		}
		// Dispatch async so the JS caller doesn't block on the channel
		// send. Send is a no-op once the program has exited.
		go p.Send(tea.WindowSizeMsg{
			Width:  args[0].Int(),
			Height: args[1].Int(),
		})
		return nil
	}))

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(out, "\r\n\x1b[31mbacio-wasm crashed: %v\x1b[0m\r\n", err)
	}
}

// readyOnce wraps the bacio TUI model to fire window.bacioReady() from
// Init, by which point the program's event loop is reading from p.msgs.
type readyOnce struct {
	tea.Model
}

func (r *readyOnce) Init() tea.Cmd {
	inner := r.Model.Init()
	ready := func() tea.Msg {
		if cb := js.Global().Get("bacioReady"); cb.Truthy() {
			cb.Invoke()
		}
		return nil
	}
	if inner == nil {
		return ready
	}
	return tea.Batch(inner, ready)
}

func (r *readyOnce) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := r.Model.Update(msg)
	r.Model = m
	return r, cmd
}

func (r *readyOnce) View() string { return r.Model.View() }
