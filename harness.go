package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// harnessPath is embedded at build time via -ldflags.
// Usage: go build -ldflags="-X 'main.harnessPath=/absolute/path/to/deepseek-harness'"
var harnessPath string

// FindHarness resolves the deepseek-harness checkout. Search order:
//  1. Build-time embedded path (ldflags -X main.harnessPath=...)
//  2. DSH_HARNESS_ROOT environment variable
//  3. Walk up from the executable
//  4. Walk up from cwd
//  5. Common locations under $HOME
func FindHarness() (string, error) {
	hasPkg := func(dir string) bool {
		_, err := os.Stat(filepath.Join(dir, "package.json"))
		return err == nil
	}

	// 1. Build-time path.
	if harnessPath != "" && hasPkg(harnessPath) {
		return harnessPath, nil
	}

	// 2. Env var.
	if root := os.Getenv("DSH_HARNESS_ROOT"); root != "" && hasPkg(root) {
		return root, nil
	}

	// 3 & 4. Walk up from executable, then from cwd.
	for _, start := range func() []string {
		var dirs []string
		if exe, err := os.Executable(); err == nil {
			dirs = append(dirs, filepath.Dir(exe))
		}
		if cwd, err := os.Getwd(); err == nil {
			dirs = append(dirs, cwd)
		}
		return dirs
	}() {
		for d := start; d != "" && d != "/"; d = filepath.Dir(d) {
			candidate := filepath.Join(d, "deepseek-harness")
			if hasPkg(candidate) {
				return candidate, nil
			}
		}
	}

	// 5. Common locations.
	home, _ := os.UserHomeDir()
	for _, d := range []string{
		filepath.Join(home, "Code", "0_Tools", "deepseek-harness"),
		filepath.Join(home, "Code", "deepseek-harness"),
		filepath.Join(home, "dev", "deepseek-harness"),
		filepath.Join(home, "Projects", "deepseek-harness"),
		filepath.Join(home, "deepseek-harness"),
	} {
		if hasPkg(d) {
			return d, nil
		}
	}

	candidates := []string{}
	if harnessPath != "" {
		candidates = append(candidates, harnessPath)
	}
	if root := os.Getenv("DSH_HARNESS_ROOT"); root != "" {
		candidates = append(candidates, root)
	}
	candidates = append(candidates,
		filepath.Join(home, "Code", "0_Tools", "deepseek-harness"),
		filepath.Join(home, "Code", "deepseek-harness"),
	)
	return "", fmt.Errorf("could not find deepseek-harness.\n"+
		"  Searched: %s\n"+
		"  Run: ln -s $(pwd) %s\n"+
		"  Or set: export DSH_HARNESS_ROOT=/actual/path/to/deepseek-harness",
		strings.Join(candidates, ", "),
		filepath.Join(home, "Code", "0_Tools", "deepseek-harness"))
}

// resolveEntrypoint returns the entrypoint path and the argv prefix for node.
// If the entrypoint ends in .js, the argv excludes --import tsx/esm and uses
// the prebuilt file. Otherwise it uses source mode with tsx.
//
// The entrypoint is taken from DSH_ENTRYPOINT (absolute, or relative to the
// harness root); the default is apps/cli/src/bin.ts.
func resolveEntrypoint(harnessRoot string) (entrypoint string, argvPrefix []string, err error) {
	ep := os.Getenv("DSH_ENTRYPOINT")
	if ep == "" {
		ep = "apps/cli/src/bin.ts"
	}
	if !filepath.IsAbs(ep) {
		ep = filepath.Join(harnessRoot, ep)
	}

	if strings.HasSuffix(ep, ".js") {
		return ep, nil, nil
	}

	// .ts entrypoint: use tsx/esm loader.
	return ep, []string{"--import", "tsx/esm"}, nil
}
