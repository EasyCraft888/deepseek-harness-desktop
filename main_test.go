package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWebURL(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantURL  string
		wantNone bool
	}{
		{
			name:    "real line",
			line:    "dsh web: http://127.0.0.1:12345/?token=abc123",
			wantURL: "http://127.0.0.1:12345/?token=abc123",
		},
		{
			name:    "LAN-suffixed variant",
			line:    "dsh web: http://192.168.1.5:12345/?token=abc",
			wantURL: "http://192.168.1.5:12345/?token=abc",
		},
		{
			name:     "browser-open line must not match",
			line:     "dsh web: opening the default browser…",
			wantNone: true,
		},
		{
			name:     "no match",
			line:     "some random output",
			wantNone: true,
		},
		{
			name:     "empty",
			line:     "",
			wantNone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWebURL(tt.line)
			if tt.wantNone {
				if got != "" {
					t.Errorf("expected no match, got %q", got)
				}
			} else if got != tt.wantURL {
				t.Errorf("got %q, want %q", got, tt.wantURL)
			}
		})
	}
}

func TestRedactToken(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "token present",
			input: "dsh web: http://127.0.0.1:12345/?token=abc123def",
			want:  "dsh web: http://127.0.0.1:12345/?token=REDACTED",
		},
		{
			name:  "token absent",
			input: "dsh web: http://127.0.0.1:12345/",
			want:  "dsh web: http://127.0.0.1:12345/",
		},
		{
			name:  "bare URL with token",
			input: "http://127.0.0.1:12345/?token=secret",
			want:  "http://127.0.0.1:12345/?token=REDACTED",
		},
		{
			name:  "token in middle of line with other params",
			input: "got url http://127.0.0.1:12345/?foo=bar&token=secret&baz=qux",
			want:  "got url http://127.0.0.1:12345/?foo=bar&token=REDACTED&baz=qux",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "no token",
			input: "just a regular log line",
			want:  "just a regular log line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactToken(tt.input)
			if got != tt.want {
				t.Errorf("redactToken(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLineWriter(t *testing.T) {
	tests := []struct {
		name     string
		writes   []string
		wantCall string // the last callback argument, or "" if none expected
	}{
		{
			name:     "single complete line",
			writes:   []string{"hello\n"},
			wantCall: "hello",
		},
		{
			name:     "line split across writes",
			writes:   []string{"hel", "lo\n"},
			wantCall: "hello",
		},
		{
			name:     "no trailing newline",
			writes:   []string{"partial"},
			wantCall: "", // callback never called
		},
		{
			name:     "CRLF",
			writes:   []string{"hello\r\n"},
			wantCall: "hello",
		},
		{
			name:     "multiple lines in one write",
			writes:   []string{"one\ntwo\n"},
			wantCall: "two",
		},
		{
			name:     "long line then newline",
			writes:   []string{strings.Repeat("x", 10000) + "\n"},
			wantCall: strings.Repeat("x", 10000),
		},
		{
			name:     "line then no newline",
			writes:   []string{"done\n", "partial"},
			wantCall: "done",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lastCall string
			cb := func(line string) {
				lastCall = line
			}
			lw := newLineWriter(cb)

			for _, w := range tt.writes {
				n, err := io.WriteString(lw, w)
				if err != nil {
					t.Fatalf("Write error: %v", err)
				}
				if n != len(w) {
					t.Fatalf("Write returned %d, want %d", n, len(w))
				}
			}

			if lastCall != tt.wantCall {
				t.Errorf("last callback = %q, want %q", lastCall, tt.wantCall)
			}
		})
	}
}

func TestResolveEntrypoint(t *testing.T) {
	dir := t.TempDir()
	tsPath := filepath.Join(dir, "bin.ts")
	jsPath := filepath.Join(dir, "bin.js")
	os.WriteFile(tsPath, nil, 0644)
	os.WriteFile(jsPath, nil, 0644)

	t.Run("default .ts entrypoint", func(t *testing.T) {
		ep, argv, err := resolveEntrypoint("/fake/harness")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(ep, "apps/cli/src/bin.ts") {
			t.Errorf("entrypoint = %q, want suffix apps/cli/src/bin.ts", ep)
		}
		if len(argv) != 2 || argv[0] != "--import" || argv[1] != "tsx/esm" {
			t.Errorf("argv = %v, want [--import tsx/esm]", argv)
		}
	})

	t.Run(".js entrypoint (no tsx)", func(t *testing.T) {
		t.Setenv("DSH_ENTRYPOINT", jsPath)
		ep, argv, err := resolveEntrypoint("/fake/harness")
		if err != nil {
			t.Fatal(err)
		}
		if ep != jsPath {
			t.Errorf("entrypoint = %q, want %q", ep, jsPath)
		}
		if len(argv) != 0 {
			t.Errorf("argv = %v, want empty (no tsx for .js)", argv)
		}
	})

	t.Run("absolute entrypoint overrides default", func(t *testing.T) {
		t.Setenv("DSH_ENTRYPOINT", tsPath)
		ep, argv, err := resolveEntrypoint("/fake/harness")
		if err != nil {
			t.Fatal(err)
		}
		if ep != tsPath {
			t.Errorf("entrypoint = %q, want %q", ep, tsPath)
		}
		if len(argv) != 2 {
			t.Errorf("argv = %v, want [--import tsx/esm]", argv)
		}
	})

	t.Run("relative entrypoint resolved against harnessRoot", func(t *testing.T) {
		t.Setenv("DSH_ENTRYPOINT", "custom/path.ts")
		ep, _, err := resolveEntrypoint("/harness/root")
		if err != nil {
			t.Fatal(err)
		}
		if ep != "/harness/root/custom/path.ts" {
			t.Errorf("entrypoint = %q, want /harness/root/custom/path.ts", ep)
		}
	})
}

func TestFindHarness(t *testing.T) {
	t.Run("harnessPath ldflag takes precedence", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "package.json"), nil, 0644)

		orig := harnessPath
		harnessPath = dir
		defer func() { harnessPath = orig }()

		got, err := FindHarness()
		if err != nil {
			t.Fatal(err)
		}
		if got != dir {
			t.Errorf("got %q, want %q", got, dir)
		}
	})

	t.Run("DSH_HARNESS_ROOT env var", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "package.json"), nil, 0644)

		orig := harnessPath
		harnessPath = ""
		defer func() { harnessPath = orig }()

		t.Setenv("DSH_HARNESS_ROOT", dir)
		got, err := FindHarness()
		if err != nil {
			t.Fatal(err)
		}
		if got != dir {
			t.Errorf("got %q, want %q", got, dir)
		}
	})

	t.Run("finds harness from common location", func(t *testing.T) {
		orig := harnessPath
		harnessPath = ""
		defer func() { harnessPath = orig }()

		t.Setenv("DSH_HARNESS_ROOT", "")

		// On a real machine with the harness checkout at a known location,
		// FindHarness should find it through common locations or walk-up.
		// We just verify it doesn't panic.
		got, err := FindHarness()
		// It's OK if it finds it or not — depends on the machine layout.
		// The contract is: if it returns a non-empty string, err must be nil.
		if err != nil && got != "" {
			t.Errorf("got %q with error %v — should return empty on error", got, err)
		}
		if err == nil && got != "" {
			// Verify package.json exists there.
			if _, err := os.Stat(filepath.Join(got, "package.json")); err != nil {
				t.Errorf("FindHarness returned %q but no package.json found there: %v", got, err)
			}
		}
	})
}

func TestMergePath(t *testing.T) {
	got := mergePath("/a:/b:/c", "/b:/d")
	// /b should be deduped; /a comes first (shell path, prepended)
	want := "/a:/b:/c:/d"
	if got != want {
		t.Errorf("mergePath = %q, want %q", got, want)
	}

	got2 := mergePath("", "/usr/bin:/bin")
	if got2 != "/usr/bin:/bin" {
		t.Errorf("mergePath with empty shell = %q, want /usr/bin:/bin", got2)
	}
}

func TestRingBuffer(t *testing.T) {
	rb := newRingBuffer(3)

	// Empty.
	if s := rb.tail(10); s != "" {
		t.Errorf("tail from empty = %q, want empty", s)
	}

	rb.add("a")
	rb.add("b")
	rb.add("c")
	rb.add("d") // wraps, drops "a"

	all := rb.all()
	if len(all) != 3 || all[0] != "b" || all[1] != "c" || all[2] != "d" {
		t.Errorf("all = %v, want [b c d]", all)
	}

	tail := rb.tail(2)
	if tail != "c\nd" {
		t.Errorf("tail(2) = %q, want c\\nd", tail)
	}
}
