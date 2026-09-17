package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// findNode locates a usable node binary. Search order:
//  1. DSH_NODE environment variable (absolute or looked up on PATH).
//  2. Login-shell PATH probe via $SHELL -l -c 'echo $PATH'.
//  3. Try PATH (possibly updated by step 2).
//  4. Hardcoded candidate list.
func findNode() (string, error) {
	// 1. Explicit override.
	if override := os.Getenv("DSH_NODE"); override != "" {
		if filepath.IsAbs(override) {
			if s, err := os.Stat(override); err == nil && !s.IsDir() {
				return override, nil
			}
			return "", fmt.Errorf("DSH_NODE %q: %w", override, os.ErrNotExist)
		}
		if p, err := exec.LookPath(override); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("DSH_NODE %q not found on PATH", override)
	}

	// 2. Login-shell PATH probe (fixes Finder's minimal PATH).
	if shellPath := os.Getenv("SHELL"); shellPath != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, shellPath, "-l", "-c", "echo $PATH")
		out, err := cmd.Output()
		if err == nil {
			merged := mergePath(strings.TrimSpace(string(out)), os.Getenv("PATH"))
			os.Setenv("PATH", merged)
		}
	}

	// 3. Try the (possibly updated) PATH first.
	if p, err := exec.LookPath("node"); err == nil {
		return p, nil
	}

	// 4. Candidate list.
	home, _ := os.UserHomeDir()
	candidates := []string{
		"/opt/homebrew/bin/node",
		"/usr/local/bin/node",
	}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "node"),
			filepath.Join(home, ".volta", "bin", "node"),
			filepath.Join(home, ".bun", "bin", "node"),
		)
	}
	for _, c := range candidates {
		if s, err := os.Stat(c); err == nil && !s.IsDir() {
			return c, nil
		}
	}

	return "", fmt.Errorf("node not found; set DSH_NODE or install Node.js")
}

// mergePath prepends the shell-provided PATH entries to the inherited PATH,
// deduplicating to keep the combined value reasonable.
func mergePath(shellPath, inheritPath string) string {
	seen := map[string]bool{}
	var out []string
	for _, d := range filepath.SplitList(shellPath) {
		d = filepath.Clean(d)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, d := range filepath.SplitList(inheritPath) {
		d = filepath.Clean(d)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return strings.Join(out, string(filepath.ListSeparator))
}
