// Bridge between Go's io.Writer/io.Reader and the JS host page.
//
// Writing: every Write call invokes a JS callback (window[name]) with a
// string. We chunk on a UTF-8 boundary so a multi-byte rune never gets
// split across two callbacks — xterm.js handles partial ANSI sequences
// but choking on half a rune leaves visible mojibake.
//
// Reading is implemented in phase 2 alongside Bubble Tea hookup.

package main

import (
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
