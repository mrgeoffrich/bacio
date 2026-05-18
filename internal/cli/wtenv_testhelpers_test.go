package cli

import "github.com/mrgeoffrich/bacio/internal/wtenv"

// testEnv builds a wtenv.Resolved suitable for unit tests that need to
// drive buildStatusReport with a fixed DB path. Keeps the legacy
// default API addr so test assertions on the API row stay stable.
func testEnv(dbPath string) wtenv.Resolved {
	return wtenv.Resolved{
		Source:  wtenv.SourceDefault,
		DBPath:  dbPath,
		APIAddr: wtenv.DefaultAPIAddr,
	}
}
