//go:build js && wasm

// Bridge between Go's io.Writer/io.Reader and the JS host page.
//
// Writing: every Write call invokes a JS callback (window[name]) with a
// string. We chunk on a UTF-8 boundary so a multi-byte rune never gets
// split across two callbacks — xterm.js handles partial ANSI sequences
// but choking on half a rune leaves visible mojibake.
//
// Reading: jsIn exposes window.bacioWriteStdin(string) which the host
// page calls on each keypress. Bytes accumulate in a small ring; Read
// blocks (via a sync.Cond) until something arrives. Phase 3 wires the
// xterm.js onData callback to bacioWriteStdin.

package main

import (
	"sync"
	"syscall/js"
	"unicode/utf8"
)

type jsOut struct {
	cbName string // window-global callback name, set once at construction
}

func newJSOut(name string) *jsOut { return &jsOut{cbName: name} }

func (w *jsOut) Write(p []byte) (int, error) {
	cb := js.Global().Get(w.cbName)
	if !cb.Truthy() {
		// No callback yet — drop silently so a missing bridge can't
		// crash the program. (Real failures will be obvious from the
		// blank terminal.)
		return len(p), nil
	}
	// Walk to a safe UTF-8 boundary at the end of the chunk so we
	// don't slice through a multi-byte sequence.
	n := safeUTF8Boundary(p)
	cb.Invoke(string(p[:n]))
	return n, nil
}

// safeUTF8Boundary returns the largest prefix of p that ends on a valid
// rune boundary. For valid UTF-8 input this returns len(p); for a
// partial trailing rune it backs off until the previous full rune.
func safeUTF8Boundary(p []byte) int {
	if utf8.Valid(p) {
		return len(p)
	}
	for n := len(p); n > 0; n-- {
		if utf8.Valid(p[:n]) {
			return n
		}
	}
	return 0
}

// jsIn is an io.Reader fed from JS via window.bacioWriteStdin.
//
// Bubble Tea's input loop calls Read in a goroutine and blocks waiting
// for keys. We use a Cond so the goroutine sleeps cheaply rather than
// spinning on the JS event loop.
type jsIn struct {
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
}

func newJSIn() *jsIn {
	r := &jsIn{}
	r.cond = sync.NewCond(&r.mu)

	// Expose window.bacioWriteStdin(stringChunk) for the JS host.
	js.Global().Set("bacioWriteStdin", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		s := args[0].String()
		if s == "" {
			return nil
		}
		r.push([]byte(s))
		return nil
	}))

	return r
}

func (r *jsIn) push(b []byte) {
	r.mu.Lock()
	r.buf = append(r.buf, b...)
	r.cond.Broadcast()
	r.mu.Unlock()
}

func (r *jsIn) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.buf) == 0 {
		// Park until JS pushes more bytes. No timeout — Bubble Tea
		// exits via tea.Quit, which closes the read loop.
		r.cond.Wait()
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
