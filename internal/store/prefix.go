package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// DerivePrefix builds a 4-char uppercase prefix from a repo name.
// Letters and digits only; pads short names with X.
func DerivePrefix(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
		if b.Len() >= 4 {
			break
		}
	}
	for b.Len() < 4 {
		b.WriteByte('X')
	}
	return b.String()
}

// AllocatePrefix returns a unique 4-char prefix derived from the candidate,
// varying the trailing characters until no clash exists.
func (s *Store) AllocatePrefix(candidate string) (string, error) {
	candidate = DerivePrefix(candidate)
	if !s.prefixTaken(candidate) {
		return candidate, nil
	}
	// Replace last char with 2..9, then last two with combinations.
	base := candidate[:3]
	for c := '2'; c <= '9'; c++ {
		try := base + string(c)
		if !s.prefixTaken(try) {
			return try, nil
		}
	}
	base2 := candidate[:2]
	for c1 := 'A'; c1 <= 'Z'; c1++ {
		for c2 := '0'; c2 <= '9'; c2++ {
			try := base2 + string(c1) + string(c2)
			if !s.prefixTaken(try) {
				return try, nil
			}
		}
	}
	return "", errors.New("could not allocate a unique prefix")
}

func (s *Store) prefixTaken(p string) bool {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM repos WHERE prefix = ?`, p).Scan(&n)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// On error, treat as taken to be safe.
		return true
	}
	return n > 0
}

// ValidatePrefix ensures a user-supplied prefix is exactly 4 alnum chars.
func ValidatePrefix(p string) (string, error) {
	if len(p) != 4 {
		return "", fmt.Errorf("prefix must be exactly 4 characters, got %q", p)
	}
	for _, r := range p {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return "", fmt.Errorf("prefix must be alphanumeric, got %q", p)
		}
	}
	return strings.ToUpper(p), nil
}
