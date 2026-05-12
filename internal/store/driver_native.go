//go:build !(js && wasm)
// +build !js !wasm

// On native targets we use modernc.org/sqlite — pure-Go, CGO-free.
// Registered as "sqlite" by the package's init.

package store

import _ "modernc.org/sqlite"
