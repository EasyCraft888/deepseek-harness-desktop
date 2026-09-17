package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// harnessPath is embedded at build time via -ldflags.
// Usage: go build -ldflags="-X 'main.harnessPath=/absolute/path/to/deepseek-harness'"
var harnessPath string

// parseWebURL extracts the local URL from dsh web's stdout line.
func parseWebURL(line string) string {
	re := regexp.MustCompile(`dsh web:\s+(http://[^\s]+)`)
	match := re.FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	return match[1]
}

// resolveBin finds a binary on PATH or at one of the given candidate paths.
// Returns the first match, falling back to the bare name.
func resolveBin(name string, candidates []string) string {
	for _, c := range candidates {
		if s, err := os.Stat(c); err == nil && !s.IsDir() {
			return c
		}
		// Also check with versioned suffixes (pnpm stores like pnpm/11.7.0/bin/pnpm).
		if entries, err := os.ReadDir(c); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					full := filepath.Join(c, e.Name(), "bin", name)
					if s, err := os.Stat(full); err == nil && !s.IsDir() {
						return full
					}
				}
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// dirOf returns the directory part of a path, empty if no separator.
func dirOf(path string) string {
	d := filepath.Dir(path)
	if d == "." || d == path {
		return ""
	}
	return d
}

// startDsh launches the DSH web server and returns its local URL.
func startDsh(harnessRoot string) (string, *exec.Cmd, error) {
	// GUI apps launched from Finder have a minimal PATH. Resolve pnpm
	// and node explicitly so the child process inherits them.
	pnpm := resolveBin("pnpm", []string{
		os.ExpandEnv("$HOME/Library/pnpm/.tools/pnpm"),
		"/opt/homebrew/bin/pnpm",
		"/usr/local/bin/pnpm",
		os.ExpandEnv("$HOME/.local/bin/pnpm"),
	})
	node := resolveBin("node", []string{
		"/opt/homebrew/bin/node",
		"/usr/local/bin/node",
		os.ExpandEnv("$HOME/.local/bin/node"),
	})

	cmd := exec.Command(pnpm, "run", "dsh", "--")
	cmd.Dir = harnessRoot
	cmd.Args = append(cmd.Args, "web", "--no-open", "--port", "0")
	// Own process group: pnpm wraps pnpm wraps node, and a signal sent to the
	// wrapper alone orphans the node server, which keeps its session locks.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Build a PATH that includes our resolved tool directories.
	pathEnv := os.Getenv("PATH")
	if dir := dirOf(pnpm); dir != "" {
		pathEnv = dir + ":" + pathEnv
	}
	if dir := dirOf(node); dir != "" {
		pathEnv = dir + ":" + pathEnv
	}
	cmd.Env = append(os.Environ(),
		"NODE_ENV=production",
		"PATH="+pathEnv,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("start dsh: %w", err)
	}

	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		found := false
		// Keep draining after the URL: an unread pipe fills and blocks dsh.
		for scanner.Scan() {
			line := scanner.Text()
			log.Println("[dsh]", line)
			if url := parseWebURL(line); url != "" && !found {
				found = true
				urlCh <- url
			}
		}
		if found {
			return
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
		} else {
			errCh <- fmt.Errorf("dsh exited without printing URL")
		}
	}()

	select {
	case url := <-urlCh:
		return url, cmd, nil
	case err := <-errCh:
		stopDsh(cmd)
		return "", nil, err
	case <-time.After(30 * time.Second):
		stopDsh(cmd)
		return "", nil, fmt.Errorf("timed out waiting for dsh web URL")
	}
}

var stopDshOnce sync.Once

// stopDsh terminates the whole dsh process group, gracefully then by force.
// Safe to call from every exit path; only the first call acts.
func stopDsh(cmd *exec.Cmd) {
	stopDshOnce.Do(func() {
		log.Println("stopping dsh web...")
		pgid := cmd.Process.Pid
		syscall.Kill(-pgid, syscall.SIGTERM)
		go cmd.Wait() // reap the wrapper so the group can empty
		// Wait on the group, not the wrapper: the wrapper exiting does not
		// prove the node server did.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if syscall.Kill(-pgid, 0) != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		syscall.Kill(-pgid, syscall.SIGKILL)
	})
}

// findHarness resolves the deepseek-harness checkout. Search order:
//  1. Build-time embedded path (ldflags -X main.harnessPath=...)
//  2. DSH_HARNESS_ROOT environment variable
//  3. Walk up from the executable
//  4. Walk up from cwd
//  5. Common locations under $HOME
func findHarness() string {
	hasPkg := func(dir string) bool {
		_, err := os.Stat(filepath.Join(dir, "package.json"))
		return err == nil
	}

	// 1. Build-time path.
	if harnessPath != "" && hasPkg(harnessPath) {
		return harnessPath
	}

	// 2. Env var.
	if root := os.Getenv("DSH_HARNESS_ROOT"); root != "" && hasPkg(root) {
		return root
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
				return candidate
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
			return d
		}
	}

	// If we still can't find it, give a clear error.
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
	log.Fatal("deepseek-harness-desktop: could not find deepseek-harness.\n" +
		"  Searched: " + strings.Join(candidates, ", ") + "\n" +
		"  Run: ln -s $(pwd) " + filepath.Join(home, "Code", "0_Tools", "deepseek-harness") + "\n" +
		"  Or set: export DSH_HARNESS_ROOT=/actual/path/to/deepseek-harness")
	panic("unreachable")
}

func main() {
	harnessRoot := findHarness()
	log.Printf("deepseek-harness-desktop: harness root at %s", harnessRoot)

	webURL, dshCmd, err := startDsh(harnessRoot)
	if err != nil {
		log.Fatalf("failed to start dsh web: %v", err)
	}
	log.Printf("dsh web is ready at %s", webURL)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		stopDsh(dshCmd)
		os.Exit(0)
	}()

	app := application.New(application.Options{
		Name:        "DeepSeek Harness",
		Description: "DeepSeek Harness desktop application",
		// Cmd+Q terminates through NSApp and may never return from app.Run,
		// so the deferred stop alone is not enough.
		OnShutdown: func() { stopDsh(dshCmd) },
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "DeepSeek Harness",
		Width:     1200,
		Height:    800,
		MinWidth:  720,
		MinHeight: 480,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              webURL,
	})

	defer stopDsh(dshCmd)

	if err := app.Run(); err != nil {
		// log.Fatal skips defers.
		stopDsh(dshCmd)
		log.Fatal(err)
	}
}