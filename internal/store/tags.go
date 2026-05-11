package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxTagLen = 80

// NormalizeTag trims surrounding whitespace and validates the result. Tags are
// case-sensitive (so "WIP" and "wip" are distinct) and may not contain inner
// whitespace, control characters, or invalid UTF-8.
func NormalizeTag(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("tag cannot be empty")
	}
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("tag is not valid UTF-8")
	}
	if len(s) > maxTagLen {
		return "", fmt.Errorf("tag too long: %d chars, max %d", len(s), maxTagLen)
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			return "", fmt.Errorf("tag %q must not contain whitespace", s)
		}
		if isDisallowedControlSingle(r) {
			return "", fmt.Errorf("tag contains a disallowed control character (U+%04X)", r)
		}
	}
	return s, nil
}

// NormalizeTags normalises and deduplicates a slice of tags, preserving the
// first occurrence's position so error messages can reference user input
// order. Returns the cleaned set in input order.
func NormalizeTags(in []string) ([]string, error) {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		n, err := NormalizeTag(t)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out, nil
}

func (s *Store) addTagsTx(tx *sql.Tx, issueID int64, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO issue_tags (issue_id, tag) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, t := range tags {
		if _, err := stmt.Exec(issueID, t); err != nil {
			return err
		}
	}
	return nil
}

// AddTagsToIssue idempotently attaches tags to an issue and bumps updated_at
// when at least one tag was new.
func (s *Store) AddTagsToIssue(issueID int64, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.addTagsTx(tx, issueID, tags); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE issues SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, issueID); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveTagsFromIssue detaches tags from an issue and bumps updated_at when
// any rows were removed.
func (s *Store) RemoveTagsFromIssue(issueID int64, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	placeholders := make([]string, len(tags))
	args := make([]any, 0, len(tags)+1)
	args = append(args, issueID)
	for i, t := range tags {
		placeholders[i] = "?"
		args = append(args, t)
	}
	q := fmt.Sprintf(`DELETE FROM issue_tags WHERE issue_id = ? AND tag IN (%s)`,
		strings.Join(placeholders, ","))
	res, err := tx.Exec(q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if _, err := tx.Exec(`UPDATE issues SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, issueID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// loadTagsForIssues returns a map of issueID → sorted tag list for the given
// IDs. Issues with no tags are absent from the map (callers should treat
// missing as empty).
func (s *Store) loadTagsForIssues(ids []int64) (map[int64][]string, error) {
	if len(ids) == 0 {
		return map[int64][]string{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf(`SELECT issue_id, tag FROM issue_tags WHERE issue_id IN (%s)`,
		strings.Join(placeholders, ","))
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64][]string, len(ids))
	for rows.Next() {
		var id int64
		var tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return nil, err
		}
		out[id] = append(out[id], tag)
	}
	for id := range out {
		sort.Strings(out[id])
	}
	return out, rows.Err()
}
