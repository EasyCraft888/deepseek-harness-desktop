package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ringBuffer keeps the last N lines of stderr for diagnostics.
type ringBuffer struct {
	lines []string
	cap   int
	pos   int
	full  bool
}

func newRingBuffer(cap int) *ringBuffer {
	return &ringBuffer{lines: make([]string, cap), cap: cap}
}

func (rb *ringBuffer) add(line string) {
	rb.lines[rb.pos] = line
	rb.pos++
	if rb.pos >= rb.cap {
		rb.pos = 0
		rb.full = true
	}
}

func (rb *ringBuffer) tail(n int) string {
	if n <= 0 {
		return ""
	}
	all := rb.all()
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return strings.Join(all, "\n")
}

func (rb *ringBuffer) all() []string {
	if !rb.full {
		return rb.lines[:rb.pos]
	}
	out := make([]string, rb.cap)
	copy(out, rb.lines[rb.pos:])
	copy(out[rb.cap-rb.pos:], rb.lines[:rb.pos])
	return out
}

// Supervisor owns the dsh child process lifecycle: spawn, readiness, crash
// detection, and graceful shutdown.
type Supervisor struct {
	cmd      *exec.Cmd
	pgid     int
	ready    chan string   // buffered 1; the readiness URL
	exited   chan struct{} // closed once Wait() returns
	waitErr  error         // written before exited closes
	stderr   *ringBuffer   // last ~50 lines, for diagnostics
	stopping atomic.Bool
	stopOnce sync.Once
	logger   *log.Logger
}

// NewSupervisor creates a Supervisor ready to launch.
func NewSupervisor(logger *log.Logger) *Supervisor {
	return &Supervisor{
		ready:  make(chan string, 1),
		exited: make(chan struct{}),
		stderr: newRingBuffer(50),
		logger: logger,
	}
}

// Start launches the dsh web server and returns its local URL.
func (s *Supervisor) Start(ctx context.Context, harnessRoot string) (string, error) {
	node, err := findNode()
	if err != nil {
		return "", fmt.Errorf("find node: %w", err)
	}
	s.logger.Printf("using node: %s", node)

	entrypoint, tsxArgv, err := resolveEntrypoint(harnessRoot)
	if err != nil {
		return "", fmt.Errorf("resolve entrypoint: %w", err)
	}
	s.logger.Printf("entrypoint: %s", entrypoint)

	argv := []string{node}
	argv = append(argv, tsxArgv...)
	argv = append(argv, entrypoint, "web", "--no-open", "--port", "0")

	s.cmd = exec.Command(argv[0], argv[1:]...)
	s.cmd.Dir = harnessRoot
	s.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Set TSX_TSCONFIG_PATH for .ts entrypoints.
	if len(tsxArgv) > 0 {
		tsconfigPath := osGetenv("TSX_TSCONFIG_PATH")
		if tsconfigPath == "" {
			tsconfigPath = harnessRoot + "/tsconfig.json"
		}
		s.cmd.Env = append(osEnviron(), "TSX_TSCONFIG_PATH="+tsconfigPath)
	}

	// Assign lineWriters — not StdoutPipe/StderrPipe. os/exec creates the
	// pipes internally and joins their copy goroutines under cmd.Wait(), so
	// there is exactly one Wait(), no reader outliving the pipe.
	stdoutReady := make(chan struct{}, 1)
	s.cmd.Stdout = newLineWriter(func(line string) {
		clean := redactToken(line)
		s.logger.Printf("[dsh] %s", clean)
		if url := parseWebURL(line); url != "" {
			select {
			case s.ready <- url:
			default:
			}
		}
	})
	s.cmd.Stderr = newLineWriter(func(line string) {
		s.logger.Printf("[dsh:err] %s", line)
		s.stderr.add(line)
	})

	// Drain stdoutReady — Start may be called multiple times (single-instance
	// re-activation path), but in practice we only call it once.
	_ = stdoutReady

	if err := s.cmd.Start(); err != nil {
		return "", fmt.Errorf("start dsh: %w", err)
	}

	s.pgid = s.cmd.Process.Pid

	// One goroutine for Wait(); closes exited when the child is reaped.
	go func() {
		s.waitErr = s.cmd.Wait()
		close(s.exited)
	}()

	// Derive a deadline from the context or DSH_STARTUP_TIMEOUT.
	deadline := 60 * time.Second
	if d, ok := ctx.Deadline(); ok {
		deadline = time.Until(d)
	}
	if s := osGetenv("DSH_STARTUP_TIMEOUT"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			deadline = d
		}
	}

	timer := time.NewTimer(deadline)
	defer timer.Stop()

	select {
	case url := <-s.ready:
		return url, nil
	case <-s.exited:
		return "", fmt.Errorf("dsh exited before printing URL: %w\nstderr:\n%s", s.waitErr, s.stderr.tail(20))
	case <-timer.C:
		s.Stop()
		return "", fmt.Errorf("timed out waiting for dsh web URL after %v\nstderr:\n%s", deadline, s.stderr.tail(20))
	}
}

// OnUnexpectedExit registers a callback invoked when the child exits without
// a prior Stop() call (i.e. a crash). Call after successful readiness.
func (s *Supervisor) OnUnexpectedExit(fn func(error, string)) {
	go func() {
		<-s.exited
		if !s.stopping.Load() {
			fn(s.waitErr, s.stderr.tail(20))
		}
	}()
}

// Stop terminates the child process group. Idempotent; safe from any exit
// path. Mirroring the harness's Electron host: SIGTERM, wait 10s, then
// SIGKILL + 5s.
func (s *Supervisor) Stop() {
	s.stopOnce.Do(func() {
		s.stopping.Store(true)

		if s.pgid == 0 {
			return
		}

		select {
		case <-s.exited:
			return
		default:
		}

		s.logger.Printf("stopping dsh web (pgid=%d)...", s.pgid)

		// Send SIGTERM once. A second signal force-exits the child
		// (see apps/cli/src/process-shutdown.ts), so never retry.
		if err := syscall.Kill(-s.pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			s.logger.Printf("SIGTERM error: %v", err)
		}

		// Wait up to 10s for orderly shutdown (> harness's own 5s dispose budget).
		select {
		case <-s.exited:
			// Success. Poll for 2s to let straggler tool subprocesses drain.
			s.logger.Printf("dsh exited gracefully; draining stragglers...")
			drainDeadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(drainDeadline) {
				if syscall.Kill(-s.pgid, 0) != nil {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
			return
		case <-time.After(10 * time.Second):
		}

		// Graceful shutdown timed out.
		s.logger.Printf("graceful shutdown timed out; force-killing process group (detached tool subprocesses may survive)")
		syscall.Kill(-s.pgid, syscall.SIGKILL)

		// Wait up to 5s for SIGKILL to take effect.
		select {
		case <-s.exited:
		case <-time.After(5 * time.Second):
			s.logger.Printf("SIGKILL did not reap child within 5s")
		}
	})
}

// parseWebURL extracts the local URL from a dsh web stdout line.
var webURLRe = regexp.MustCompile(`dsh web:\s+(http://[^\s]+)`)

func parseWebURL(line string) string {
	match := webURLRe.FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	return match[1]
}

// osGetenv is os.Getenv; extracted for testability.
var osGetenv = os.Getenv

// osEnviron is os.Environ; extracted for testability.
var osEnviron = os.Environ
