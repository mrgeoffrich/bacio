package sync

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"
)

// Redirect describes a single label move recorded in
// repos/<prefix>/redirects.yaml. The on-disk file is a
// chronologically-sorted top-level YAML sequence of these. Every
// renumber (issue) and every rename (feature/document) appends a
// new entry; nothing is ever pruned in v1.
//
// `Reason` is one of the small set the design doc spelled out:
// "label_collision" (the import side gives up the label to a
// just-imported uuid that's already in git), "manual_rename"
// (the user explicitly renamed in DB, propagated on next export),
// or "label_changed_remotely" (an incoming file uses a different
// label for a uuid bacio already knows about).
//
// UUID is the canonical identity — chains of (old → new) for the
// same uuid resolve forward to the current label.
type Redirect struct {
	Kind      string    `json:"kind"`
	Old       string    `json:"old"`
	New       string    `json:"new"`
	UUID      string    `json:"uuid"`
	ChangedAt time.Time `json:"changed_at"`
	Reason    string    `json:"reason"`
}

// Allowed reason values, mirroring the design doc.
const (
	ReasonLabelCollision      = "label_collision"
	ReasonManualRename        = "manual_rename"
	ReasonLabelChangedRemotely = "label_changed_remotely"
)

// RedirectsFile is the relative slash-separated path of the redirects
// file inside a per-repo folder. Used by both load and append so the
// "where does this live" answer has a single source of truth.
func RedirectsFile(prefix string) string {
	return path.Join(RepoFolder(prefix), "redirects.yaml")
}

// LoadRedirects reads repos/<prefix>/redirects.yaml from `target`
// (the sync repo root) and returns the parsed entries. A missing
// file is fine — returns an empty slice and no error. The strict
// parser handles unknown fields and type mismatches.
//
// Entries are returned in the order they appear in the file. The
// emit side keeps them sorted by ChangedAt; importers rely on this
// during forward-chase resolution but tolerate any order (the chain
// follows uuid references, not file order).
func LoadRedirects(target, prefix string) ([]Redirect, error) {
	abs := filepath.Join(target, filepath.FromSlash(RedirectsFile(prefix)))
	b, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read redirects: %w", err)
	}
	parsed, err := ParseRedirectsYAML(b)
	if err != nil {
		return nil, err
	}
	out := make([]Redirect, len(parsed))
	for i, p := range parsed {
		out[i] = Redirect{
			Kind:      p.Kind,
			Old:       p.Old,
			New:       p.New,
			UUID:      p.UUID,
			ChangedAt: p.ChangedAt,
			Reason:    p.Reason,
		}
	}
	return out, nil
}

// AppendRedirect adds a new entry to repos/<prefix>/redirects.yaml in
// `target`, preserving sort order by ChangedAt. The file is
// rewritten via the canonical emitter so it stays hash-stable and
// human-reviewable. If the file doesn't exist yet, it's created.
//
// Duplicates (same uuid + old + new) are deliberately allowed — the
// caller decides whether to dedupe; for instance, two import passes
// resolving the same collision should each record an entry.
func AppendRedirect(target, prefix string, r Redirect) error {
	existing, err := LoadRedirects(target, prefix)
	if err != nil {
		return err
	}
	existing = append(existing, r)
	sort.Slice(existing, func(i, j int) bool {
		// Stable order: ChangedAt first, then UUID, then Old →
		// reproducible across runs even if two entries share a
		// timestamp.
		if !existing[i].ChangedAt.Equal(existing[j].ChangedAt) {
			return existing[i].ChangedAt.Before(existing[j].ChangedAt)
		}
		if existing[i].UUID != existing[j].UUID {
			return existing[i].UUID < existing[j].UUID
		}
		return existing[i].Old < existing[j].Old
	})
	bytes, err := Emit(BuildRedirectYAML(existing))
	if err != nil {
		return fmt.Errorf("emit redirects: %w", err)
	}
	abs := filepath.Join(target, filepath.FromSlash(RedirectsFile(prefix)))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("mkdir redirects: %w", err)
	}
	if err := os.WriteFile(abs, bytes, 0o644); err != nil {
		return fmt.Errorf("write redirects: %w", err)
	}
	return nil
}

// BuildRedirectYAML produces the canonical Node tree for a sorted
// redirect list. Exposed so the import pipeline can compute the
// final YAML bytes at the same moment as it persists the DB-side
// changes (writing to disk happens later, when staging is real).
func BuildRedirectYAML(rs []Redirect) Node {
	items := make([]Node, len(rs))
	for i, r := range rs {
		items[i] = Map(
			Pair{"changed_at", Time(r.ChangedAt)},
			Pair{"kind", Str(r.Kind)},
			Pair{"new", Str(r.New)},
			Pair{"old", Str(r.Old)},
			Pair{"reason", Str(r.Reason)},
			Pair{"uuid", Str(r.UUID)},
		)
	}
	return Seq(items...)
}

// ResolveLabel walks the redirect list to find the current label for
// (kind, label). Returns (currentLabel, true) when at least one move
// matched, and ("", false) when the input label has no entries.
//
// A redirect chain can be multi-hop: A → B → C (same uuid). The
// resolver follows by uuid, not by label, so it terminates correctly
// even if a user reuses old labels for new records (which the
// collision rule already forbids in steady state, but defensive code
// is cheap).
//
// Cycles (rare and only possible from manual file editing) are
// detected via a seen-uuids set; if we'd revisit a uuid the chain
// is truncated at the last good label and (label, true) is
// returned. We don't error out — bad input shouldn't break
// `bacio issue show OLD-7`.
func ResolveLabel(rs []Redirect, kind, label string) (string, bool) {
	// First hop: find the entry whose `old` matches the input. If
	// multiple match (label reused over time, normally impossible),
	// pick the earliest by ChangedAt — that's the historical "old"
	// pointer; later entries will be revisited via the uuid chain.
	var current *Redirect
	for i := range rs {
		if rs[i].Kind != kind || rs[i].Old != label {
			continue
		}
		if current == nil || rs[i].ChangedAt.Before(current.ChangedAt) {
			current = &rs[i]
		}
	}
	if current == nil {
		return "", false
	}
	uuid := current.UUID
	currentLabel := current.New

	// Subsequent hops: chase by uuid, picking the latest "old =
	// currentLabel" entry's New. Cycle break: bail if we revisit a
	// (uuid, oldLabel) pair.
	seen := map[string]bool{currentLabel: true}
	for {
		var next *Redirect
		for i := range rs {
			if rs[i].UUID != uuid || rs[i].Kind != kind {
				continue
			}
			if rs[i].Old != currentLabel {
				continue
			}
			if next == nil || rs[i].ChangedAt.After(next.ChangedAt) {
				next = &rs[i]
			}
		}
		if next == nil {
			break
		}
		if seen[next.New] {
			// Cycle — return what we have rather than spinning.
			break
		}
		currentLabel = next.New
		seen[currentLabel] = true
	}
	return currentLabel, true
}
