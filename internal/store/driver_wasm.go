//go:build js && wasm
// +build js,wasm

// On the WASM build we can't use modernc.org/sqlite — its libc
// dependency has no js/wasm capi files. Switch to
// github.com/ncruces/go-sqlite3, which ships a pure-Go SQLite
// translation that compiles on any Go target.
//
// The upstream driver registers as "sqlite3" by default; we register
// it under "sqlite" too so internal/store/store.go can keep using
// sql.Open("sqlite", ...) without a build-tagged code path.

package store

import (
	"database/sql"

	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed" // bundles the SQLite WASM build
)

func init() {
	for _, name := range sql.Drivers() {
		if name == "sqlite" {
			return // already registered (some other init beat us to it)
		}
	}
	sql.Register("sqlite", &driver.SQLite{})
}
