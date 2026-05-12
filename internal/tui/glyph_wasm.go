//go:build js && wasm
// +build js,wasm

package tui

// xterm.js in the browser doesn't ship Nerd Fonts, so the Powerline
// glyph the native build uses renders as tofu. "⎇" (U+2387, ALTERNATIVE
// KEY SYMBOL) is in every BMP font and reads as "git-ish branch" in
// context.
const repoGlyph = "⎇"
