//go:build js && wasm
// +build js,wasm

package tea

import (
	"errors"
	"os"
)

// initInput is a no-op for js/wasm — the input comes from an in-process
// io.Reader set via tea.WithInput(), there's no TTY to put into raw mode.
func (p *Program) initInput() error { return nil }

// openInputTTY is not available in the browser. Bubble Tea only calls
// this when the caller hasn't provided WithInput; the bacio-wasm entry
// always provides one, so this path is unreachable in practice.
func openInputTTY() (*os.File, error) {
	return nil, errors.New("bubbletea: opening a TTY is not supported on js/wasm")
}

const suspendSupported = false

// suspendProcess can't ship a SIGTSTP signal in a browser sandbox.
func suspendProcess() {}
