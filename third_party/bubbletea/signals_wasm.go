//go:build js && wasm
// +build js,wasm

package tea

// listenForResize is a no-op on js/wasm — terminal-size changes arrive
// from the xterm.js host as synthetic WindowSizeMsg values pushed via
// the input bridge, rather than via SIGWINCH. The host is responsible
// for sending those messages on resize.
func (p *Program) listenForResize(done chan struct{}) {
	close(done)
}
