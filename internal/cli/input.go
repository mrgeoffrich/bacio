package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// readLongText resolves a long-text input from either an inline value (which
// may be "-" for stdin) or a file path. Exactly one source must be provided
// when required is true. When required is false, returns "" if neither is set.
func readLongText(inline, file string, required bool, fieldName string) (string, error) {
	switch {
	case inline != "" && file != "":
		return "", fmt.Errorf("--%s and --%s-file are mutually exclusive", fieldName, fieldName)
	case file != "":
		return readFile(file)
	case inline == "-":
		return readAll(os.Stdin)
	case inline != "":
		return inline, nil
	case required:
		return "", fmt.Errorf("provide --%s - (stdin) or --%s-file <path>", fieldName, fieldName)
	default:
		return "", nil
	}
}

func readFile(path string) (string, error) {
	if path == "-" {
		return readAll(os.Stdin)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func readAll(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return string(b), nil
}
