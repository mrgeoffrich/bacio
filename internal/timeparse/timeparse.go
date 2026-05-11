// Package timeparse holds the lookback / timestamp parsers shared between
// `bacio history` (CLI) and the HTTP history endpoints. Day (d) and week (w)
// suffixes are not understood by time.ParseDuration, so we wrap it.
package timeparse

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Lookback extends time.ParseDuration with day (d) and week (w) units.
// Examples: 30m, 1h, 6h, 1d, 7d, 2w. Single-unit only for d/w; multi-unit
// composites use Go's native parser.
func Lookback(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("duration is required (e.g. 30m, 1h, 1d, 2w)")
	}
	last := s[len(s)-1]
	switch last {
	case 'd', 'w':
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q (e.g. 1d, 2w)", s)
		}
		mult := 24 * time.Hour
		if last == 'w' {
			mult = 7 * 24 * time.Hour
		}
		return time.Duration(n) * mult, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (e.g. 30m, 1h, 1d, 2w)", s)
	}
	return d, nil
}

// Timestamp accepts several common shapes and returns a UTC time.
// Date-only and bare datetimes are interpreted in the user's local
// timezone; RFC 3339 inputs honour the offset they specify.
//
// Accepted: 2006-01-02 | 2006-01-02 15:04 | 2006-01-02 15:04:05 |
//
//	2006-01-02T15:04:05 | RFC3339 (e.g. 2026-05-03T07:27:14Z).
func Timestamp(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("timestamp is required")
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q (try YYYY-MM-DD, YYYY-MM-DD HH:MM, or RFC 3339)", s)
}
