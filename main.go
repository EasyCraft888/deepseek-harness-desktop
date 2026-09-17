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
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// parseWebURL extracts the local URL from dsh web's stdout line like:
//
//	dsh web: http://127.0.0.1:3080/?token=...
func parseWebURL(line string) string {
	re := regexp.MustCompile(`dsh web:\s+(http://[^\s]+)`)
	match := re.FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	return match[1]
}

// startDsh launches the DSH web server from the harness checkout and returns
// its local URL. The caller owns the returned *exec.Cmd and must wait on it.
func startDsh(harnessRoot string) (string, *exec.Cmd, error) {
	cmd := exec.Command("pnpm", "run", "dsh", "--")
	cmd.Dir = harnessRoot
	// --port 0: let the OS pick a free port (avoids conflict with any
	// already-running dsh web on the default 3080).
	cmd.Args = append(cmd.Args, "web", "--no-open", "--port", "0")
	cmd.Env = append(os.Environ(), "NODE_ENV=production")

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
		for scanner.Scan() {
			line := scanner.Text()
			log.Println("[dsh]", line)
			if url := parseWebURL(line); url != "" {
				urlCh <- url
				return
			}
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
		cmd.Process.Kill()
		return "", nil, err
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		return "", nil, fmt.Errorf("timed out waiting for dsh web URL")
	}
}

func main() {
	// Resolve the harness checkout. Search order:
	//   1. Path from the binary location (needed when launched from .app bundle)
	//   2. Walk up from the current working directory (handy for `go run`)
	// Expected layout:
	//   <workspace>/
	//     deepseek-harness/          ← the main repo
	//     deepseek-harness-desktop/  ← this project (or .app bundle)
	harnessRoot := ""

	// Try binary-relative first.
	if exe, err := os.Executable(); err == nil {
		harnessRoot = filepath.Dir(exe)
	}
	if harnessRoot == "" {
		harnessRoot, _ = os.Getwd()
	}

	for {
		candidate := harnessRoot + "/deepseek-harness/package.json"
		if _, err := os.Stat(candidate); err == nil {
			harnessRoot += "/deepseek-harness"
			break
		}
		parent := filepath.Dir(harnessRoot)
		if parent == harnessRoot {
			log.Fatal("deepseek-harness-desktop: could not find deepseek-harness checkout")
		}
		harnessRoot = parent
	}

	log.Printf("deepseek-harness-desktop: harness root at %s", harnessRoot)

	webURL, dshCmd, err := startDsh(harnessRoot)
	if err != nil {
		log.Fatalf("failed to start dsh web: %v", err)
	}
	log.Printf("dsh web is ready at %s", webURL)

	// Clean up the DSH process on exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("stopping dsh web...")
		dshCmd.Process.Signal(os.Interrupt)
		time.Sleep(2 * time.Second)
		dshCmd.Process.Kill()
	}()

	app := application.New(application.Options{
		Name:        "DeepSeek Harness",
		Description: "DeepSeek Harness desktop application",
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

	// When app.Run returns (last window closed), stop DSH.
	defer func() {
		log.Println("stopping dsh web...")
		dshCmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			dshCmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			dshCmd.Process.Kill()
		}
	}()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}