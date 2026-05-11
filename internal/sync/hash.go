package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

// ContentHash returns the canonical "sha256:<hex>" identity of a byte
// blob. The emitter is what makes the input canonical (sorted keys,
// LF-only, single trailing newline, always-quoted user strings); this
// function is the thin wrapper that turns those bytes into the value
// stored in `sync_state.last_synced_hash` and embedded as
// `description_hash` / `body_hash` / `content_hash` in the YAML.
//
// We prefix with the algorithm name so a future rotation to (say)
// blake3 stays observable on disk without a schema change.
func ContentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// NormalizeBody coerces CRLF and bare-CR line endings to LF so that
// hash computation is invariant under git's autocrlf checkout
// behaviour. Bodies (description.md, comment .md, doc content.md) are
// the user-supplied free-form fields where this can bite: a sync repo
// cloned on Windows with default core.autocrlf=true comes back as
// CRLF, and re-hashing that on import would not match the LF bytes we
// originally wrote.
//
// Apply this to body bytes before both writing them to disk and
// hashing them, so the hash stored in YAML matches the on-disk bytes
// regardless of how git delivered the working tree.
func NormalizeBody(b []byte) []byte {
	// Replace CRLF first (so the lone-CR pass below doesn't double-step
	// over the \r in CRLF).
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	b = bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
	return b
}
