package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// jsonInputFlag is the canonical flag name (`--json`, `-j`) every mutating
// command uses to accept a JSON payload instead of typed flags / positionals.
const jsonInputFlag = "json"

// readJSONInput resolves the value of --json into raw JSON bytes.
//
// Accepted forms:
//   - "-"      read from stdin
//   - "@path"  read from a file
//   - inline   anything else is treated as JSON text
//
// Returns (nil, nil) when value is empty, signalling that --json was not set.
func readJSONInput(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	if value == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read --json from stdin: %w", err)
		}
		return b, nil
	}
	if strings.HasPrefix(value, "@") {
		path := value[1:]
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read --json from %s: %w", path, err)
		}
		return b, nil
	}
	return []byte(value), nil
}

// addInputFlag registers --json/-j on cmd, storing the raw value in *target.
func addInputFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, jsonInputFlag, "j", "",
		`JSON payload (inline text, "-" for stdin, or "@path/to.json"); mutually exclusive with positionals/flags`)
}

// rejectMixedInput returns an error if --json was supplied alongside any
// positional argument or any of the named flags. Each command lists the
// per-field flags it owns so a friendlier error names the specific conflict.
func rejectMixedInput(cmd *cobra.Command, args []string, conflictingFlags ...string) error {
	if len(args) > 0 {
		return fmt.Errorf("--json cannot be combined with positional arguments")
	}
	for _, name := range conflictingFlags {
		if cmd.Flags().Changed(name) {
			return fmt.Errorf("--json cannot be combined with --%s", name)
		}
	}
	return nil
}
