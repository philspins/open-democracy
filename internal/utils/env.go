package utils

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv reads KEY=VALUE pairs from a .env-style file at path.
// If the file does not exist, it returns nil without error.
// Existing process environment variables are never overwritten.
// Inline comments, blank lines, and quoted values (single or double) are supported.
//
// LoadDotEnv calls os.Setenv and is therefore not safe to call concurrently.
// It should be invoked once during application initialisation, before any
// goroutines that read environment variables are started.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		// Strip inline comments (e.g. KEY=value # comment) for unquoted values.
		// A space before '#' is required so values containing '#' naturally
		// (e.g. URLs with fragments) are not accidentally truncated.
		// For quoted values the comment must appear after the closing quote.
		if len(val) > 0 && val[0] != '"' && val[0] != '\'' {
			if i := strings.Index(val, " #"); i >= 0 {
				val = strings.TrimSpace(val[:i])
			}
		}
		if len(val) >= 2 {
			q := val[0]
			if (q == '"' || q == '\'') && val[len(val)-1] == q {
				val = val[1 : len(val)-1]
			}
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return err
		}
	}
	return scanner.Err()
}
