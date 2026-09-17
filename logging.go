package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var tokenRe = regexp.MustCompile(`(token=)[^&\s]+`)

// redactToken replaces any "token=<value>" fragment with "token=REDACTED".
// It handles both standalone URLs and log lines containing a URL.
func redactToken(s string) string {
	return tokenRe.ReplaceAllString(s, "token=REDACTED")
}

// openLogFile opens (or creates) the log file at ~/Library/Logs/DeepSeek Harness/app.log.
// If the file already exceeds 5 MB it is truncated.
func openLogFile() (*os.File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "Library", "Logs", "DeepSeek Harness")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "app.log")

	// Truncate if too large (no rotation — just a size cap).
	if fi, err := os.Stat(p); err == nil && fi.Size() > 5*1024*1024 {
		_ = os.Remove(p)
	}

	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	// Ensure parent directory exists (re-check in case it was removed).
	if err := os.MkdirAll(dir, 0755); err != nil {
		f.Close()
		return nil, err
	}

	return f, nil
}

// newLogger creates a log.Logger that writes to both stderr and the log file.
// If the log file cannot be opened, writes go to stderr alone.
func newLogger() *log.Logger {
	f, err := openLogFile()
	if err != nil {
		return log.New(os.Stderr, "", log.LstdFlags)
	}
	return log.New(io.MultiWriter(os.Stderr, f), "", log.LstdFlags)
}

// stderrTail returns the last n lines from a string as a single joined string.
func stderrTail(raw string, n int) string {
	lines := strings.Split(raw, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
