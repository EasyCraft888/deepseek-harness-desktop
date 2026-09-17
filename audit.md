# Audit & fix: deepseek-harness-desktop

## Context

`deepseek-harness-desktop` is a Wails v3 wrapper that turns the `dsh web` server
into a native macOS app: it spawns the server as a child process, scrapes the
local URL from its stdout, and points a WebView at it.

It works on a happy path, but the subprocess layer *is* the product, and that is
where the defects are. Today the app can leak an orphaned Node server and its
tool subprocesses on exit, silently vanishes when startup fails, races its own
signal handler against the framework's, and its build script is broken outright.

This plan fixes the process lifecycle start-to-end — spawn, readiness, crash
detection, graceful shutdown — and splits the single 300-line `main.go` into
focused files, without growing the project's scope.

### Decisions taken

- **The Wails wrapper stays.** The harness monorepo does ship `apps/desktop` (a
  full Electron shell), but packaging it for macOS has no unsigned path
  (`apps/desktop/scripts/package-target.ts:207` — `--unsigned requires win-x64`)
  and demands a signing identity, Team ID and notarization credentials, i.e. a
  paid Apple Developer membership. `DSH_DESKTOP_APP_ID` itself is free — just a
  reverse-DNS string you pick — so it is not the blocker. A system-WebView
  wrapper stays worthwhile.
- **Spawn `node` directly** rather than `pnpm run dsh --`, with the entrypoint
  overridable by env var.
- **Full fix**, split into focused files.

---

## Audit findings

Severity: **[H]** breaks or leaks · **[M]** wrong under realistic conditions ·
**[L]** hygiene.

### Process lifecycle

1. **[H] Graceful shutdown almost always escalates to `SIGKILL`, leaking tool
   subprocesses.** `stopDsh` (`main.go:155`) allows 5s before `SIGKILL`. The
   harness's own dispose budget is exactly 5000ms
   (`apps/cli/src/process-shutdown.ts`), so our deadline expires as the child is
   still cleaning up. This matters more than it looks: the harness spawns tool
   subprocesses (bash, MCP stdio servers, LSP) with `detached: true` on macOS
   (`packages/subprocess/subprocess-local/src/spawn.ts:465`), so they live in
   *their own process groups*. Killing the dsh process group does **not** reach
   them — only a completed graceful dispose does. The harness's own Electron app
   allows 10s, then `SIGKILL`, then 5s more.

2. **[H] `cmd.Wait()` races the stdout reader.** `stopDsh` calls `go cmd.Wait()`
   (`main.go:160`) while the scanner goroutine from `startDsh` still reads
   `cmd.StdoutPipe()`. `os/exec` documents this as incorrect: `Wait` closes the
   pipe once the process exits, so the reader can hit a closed descriptor.

3. **[H] Two competing signal handlers.** `main.go:258-264` installs
   `signal.Notify` + `os.Exit(0)`, but Wails already installs its own
   SIGINT/SIGTERM handler calling `app.Quit()`
   (`pkg/application/signal_handler_desktop.go`; suppressed only by
   `DisableDefaultSignalHandler`). Both fire, and `os.Exit(0)` can cut the
   graceful path short.

4. **[M] The child dying is never noticed.** Nothing waits on the process after
   startup. If the Node server crashes, the window silently shows a connection
   error and the app carries on as if healthy.

5. **[M] A retry loop would force-exit the child.** Any future retry must not
   re-send `SIGTERM`: a second signal while a shutdown is pending triggers an
   immediate force-exit (`apps/cli/src/process-shutdown.ts`). Worth an explicit
   comment so nobody "improves" it later.

6. **[L]** Package-scope `sync.Once` for stop; `syscall.Kill` errors discarded;
   `defer stopDsh` in `main` is dead once `OnShutdown` is wired.

### Startup & failure reporting

7. **[H] Startup failures are invisible in a bundled `.app`.** `findHarness`
   calls `log.Fatal` (`main.go:241`) and `main` calls `log.Fatalf`
   (`main.go:254`). Launched from Finder there is no attached terminal, so the
   app simply never appears, with no explanation. The existing
   `frontend/dist/index.html` loading page is never used — the window is built
   with the final URL, so nothing can be shown before the server is ready.

8. **[M] `stderr` is not captured, so the most likely failure is unexplained.**
   `cmd.Stderr = os.Stderr` (`main.go:108`). The harness's realistic failure is a
   missing build artifact: it prints ``run `pnpm run build` before launch`` to
   stderr and exits without a URL. The wrapper reports only `timed out waiting
   for dsh web URL`.

9. **[M] 30s timeout is too tight.** The default entrypoint is TypeScript run
   through `tsx`, compiled on the fly; cold starts can exceed it.

10. **[M] No single-instance guard.** Launching twice starts two servers. Wails
    supports this natively via `Options.SingleInstance`, but the lock is taken
    *inside* `application.New`, which the current ordering (subprocess first)
    defeats.

### Toolchain resolution

11. **[H] `resolveBin` picks the wrong pnpm.** Its first candidate,
    `~/Library/pnpm/.tools/pnpm`, is a directory of *versions*. The `ReadDir`
    loop (`main.go:42`) returns the lexically first entry with a `bin/` — on this
    machine **10.17.0**, not the user's real pnpm (10.18.0 on `PATH`) nor the
    version the harness pins (`packageManager: pnpm@11.7.0`). Silent,
    non-deterministic toolchain selection.

12. **[M] Hardcoded candidate lists miss every version manager** (nvm, fnm, asdf,
    volta, mise). The standard fix for "Finder gives a minimal PATH" is to probe
    the login shell — `$SHELL -l -c 'echo $PATH'`, verified working here.

13. **[L]** `resolveBin` falls back to the bare name, deferring failure to a
    confusing `exec` error instead of reporting "node not found".

### Security / privacy

14. **[M] The auth token is logged verbatim.** The readiness line is
    `dsh web: http://127.0.0.1:<port>/?token=<base64url>`; `main.go:123` logs
    every child line as-is. Tolerable on a terminal, not once logs go to a file.

### Build & packaging

15. **[H] `build.sh` is broken.** The SVG source moved to
    `$PROJECT_DIR/icon.svg`, but line 29 still reads
    `SRC="$ICONSET/favicon.svg.png"`. `qlmanage` names its output after the input
    basename, so it now writes `icon.svg.png` and the script dies at "qlmanage
    did not produce output". It passes today only because a stale
    `favicon.svg.png` predates the rename — and `rm -rf "$ICONSET"` deletes even
    that on the next clean run.

16. **[M] `Taskfile.yml` is entirely dead.** Every `includes:` target
    (`build/Taskfile.yml`, `build/darwin/…`, `build/config.yml`, …) is missing,
    so `task build` fails immediately. Leftover Wails scaffolding.

17. **[L]** Stray `gitignore` (no dot) duplicating `.gitignore`; Info.plist lacks
    `NSAllowsLocalNetworking`; bundle ID is `com.deepseek.harness` although the
    repo is `EasyCraft888`-owned; README documents the old `pnpm run dsh` launch
    and "Go 1.22+" against `go.mod`'s `go 1.25.0`.

18. **[L] Zero tests.**

---

## Implementation

### File layout

`main.go` shrinks to wiring only; each new file owns one concern.

| File | Responsibility |
| --- | --- |
| `main.go` | Wire logging → app → window → async start → shutdown hook. No logic. |
| `supervisor.go` | `Supervisor`: spawn, readiness, crash watch, graceful stop. |
| `linewriter.go` | `lineWriter` — an `io.Writer` that invokes a callback per complete line. |
| `harness.go` | `FindHarness()`, `resolveEntrypoint()`. |
| `toolchain.go` | Login-shell PATH probe, `findNode()`. |
| `logging.go` | File+stderr logger, `redactToken()`. |
| `assets.go` | `//go:embed frontend/dist` for the loading page. |

### Supervisor

The central fix. One type owning the whole child lifecycle:

```go
type Supervisor struct {
    cmd      *exec.Cmd
    pgid     int
    ready    chan string   // buffered 1; the readiness URL
    exited   chan struct{} // closed once Wait() returns
    waitErr  error         // written before exited closes
    stderr   *ringBuffer   // last ~50 lines, for diagnostics
    stopping atomic.Bool
    stopOnce sync.Once
}

func NewSupervisor(cfg Config) *Supervisor
func (s *Supervisor) Start(ctx context.Context) (string, error) // returns the URL
func (s *Supervisor) OnUnexpectedExit(fn func(error, string))
func (s *Supervisor) Stop()                                     // idempotent
```

**No `StdoutPipe`.** This removes finding 2 by construction: assign
`cmd.Stdout` / `cmd.Stderr` to `lineWriter` values. Because these are not
`*os.File`, `os/exec` creates the pipes itself and spawns copy goroutines that
`cmd.Wait()` joins — so there is exactly one `Wait()`, no reader outliving the
pipe, and no `found`/drain bookkeeping. The stdout callback logs the redacted
line and does one non-blocking send of the matched URL to `ready`; the stderr
callback logs and appends to the ring buffer.

**Start:**
1. `findNode()`, build argv (below), `cmd.Dir = harnessRoot`,
   `SysProcAttr{Setpgid: true}` (kept: node still spawns children).
2. `cmd.Start()`; `pgid = cmd.Process.Pid`.
3. One goroutine: `s.waitErr = cmd.Wait(); close(s.exited)`.
4. `select` on `ready` / `exited` / `ctx.Done()` (60s, `DSH_STARTUP_TIMEOUT`
   overridable):
   - `ready` → return the URL.
   - `exited` → error carrying the exit status **and the stderr tail**.
   - timeout → `Stop()`, error carrying the stderr tail.

**Stop** — `sync.Once`, safe from any exit path, mirroring the harness's own
Electron host:
1. `stopping.Store(true)` so the exit watcher does not report a crash.
2. Return immediately if `exited` is already closed.
3. `syscall.Kill(-pgid, SIGTERM)` **once** — with a comment that a second signal
   force-exits the child (finding 5). Log a non-`ESRCH` error.
4. Wait on `exited`, deadline **10s** (> the child's own 5s dispose budget).
5. On success, poll `kill(-pgid, 0)` for up to 2s to let stragglers drain, then
   return.
6. On timeout, log a warning that detached tool subprocesses may survive, then
   `SIGKILL` the group and wait up to 5s more.

**Crash watch:** after readiness, a goroutine blocks on `exited`; if
`stopping` is false it invokes the `OnUnexpectedExit` callback with the wait
error and the stderr tail.

### Startup orchestration

Ordering is forced by two verified constraints: the single-instance lock is
acquired *inside* `application.New`, and `InvokeSync` (used by `SetURL` and
`Dialog.Show`) blocks until the main loop is running.

```go
sup := NewSupervisor(cfg)          // constructed early so OnShutdown can close over it
app := application.New(application.Options{
    SingleInstance: &application.SingleInstanceOptions{UniqueID: "dev.easycraft.deepseek-harness-desktop"},
    Assets:         application.AssetOptions{Handler: application.BundledAssetFileServer(loadingAssets)},
    OnShutdown:     sup.Stop,       // Cmd+Q may never return from app.Run
    Mac:            application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
})
win := app.Window.NewWithOptions(... URL: "/" ...)   // embedded loading page

app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
    go startup(app, win, sup)       // only now is InvokeSync safe
})
app.Run()
```

`startup` resolves the harness, calls `sup.Start`, then `win.SetURL(url)`, and
registers `sup.OnUnexpectedExit`. Every failure path goes through one helper
that logs, shows `app.Dialog.Error()`, and calls `app.Quit()` — so a
Finder-launched app explains itself instead of vanishing (finding 7). The dialog
message includes the captured stderr tail (finding 8).

`Stop()` being idempotent means `OnShutdown` is the single shutdown path; the
manual `signal.Notify` goroutine and the redundant `defer` are deleted (finding
3), leaving Wails' own handler to drive `app.Quit()` → `OnShutdown` → `Stop()`.

### Spawning node directly

Replaces `pnpm run dsh --`, removing two wrapper processes and the pnpm version
ambiguity. Verified working: `node --import tsx/esm apps/cli/src/bin.ts web
--help` responds in 1.2s.

```
<node> --import tsx/esm <entrypoint> web --no-open --port 0
```

- Entrypoint from `DSH_ENTRYPOINT` (absolute, or relative to the harness root),
  default `apps/cli/src/bin.ts`. If it ends in `.js`, drop `--import tsx/esm` —
  the prebuilt `apps/cli/lib/bin.js` exists and starts faster.
- Set `TSX_TSCONFIG_PATH=<root>/tsconfig.json` for `.ts` entrypoints unless
  already set (the harness's own e2e tests set it explicitly).
- Keep `--port 0`: an OS-assigned port makes `EADDRINUSE` impossible.
- **Drop `NODE_ENV=production`** (`main.go:100`). It is unexplained, and the
  harness's own `pnpm run dsh` does not set it; matching terminal behaviour is
  the safer default.
- `cmd.Dir = harnessRoot` stays — the harness auto-loads `.env` from the process
  cwd, so this is load-bearing.

### Toolchain

Replace `resolveBin` (findings 11-13) with a login-shell probe in
`toolchain.go`: run `$SHELL -l -c 'echo $PATH'` with a 5s timeout, merge the
result ahead of the inherited `PATH`, and `os.Setenv("PATH", merged)` once at
startup — so both `exec.LookPath("node")` and the child's inherited environment
get the right one. Fall back to a small candidate list
(`/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `~/.volta/bin`,
`~/.bun/bin`) if the probe fails, and honour a `DSH_NODE` override. `findNode`
returns a real error naming what was searched, never a bare `"node"`.

`FindHarness` in `harness.go` keeps today's search order (build-time ldflag →
`DSH_HARNESS_ROOT` → walk up from exe/cwd → common `$HOME` locations) but
**returns `(string, error)`** instead of calling `log.Fatal`.

### Logging

`logging.go` writes to `io.MultiWriter(os.Stderr, file)` with the file at
`~/Library/Logs/DeepSeek Harness/app.log`, truncated at startup if it exceeds
5 MB (a size cap, not a rotation library). `redactToken()` rewrites any
`token=<value>` to `token=REDACTED` and is applied to every child line and to any
URL that is logged (finding 14); the real URL is only ever held in memory.

### Build & repo hygiene

- `build.sh`: derive the filename — `SRC="$ICONSET/$(basename "$SVG").png"`
  (finding 15); fail with a clear message if `../deepseek-harness` is absent;
  add `NSAppTransportSecurity` → `NSAllowsLocalNetworking` to `Info.plist`; set
  `CFBundleIdentifier` to `dev.easycraft.deepseek-harness-desktop`, matching the
  single-instance `UniqueID`. Keep the `-X main.harnessPath` ldflag.
- Delete `Taskfile.yml` (every include missing) and the stray `gitignore`.
- Update `README.md`: Go 1.25, the node-direct launch, `DSH_ENTRYPOINT` /
  `DSH_HARNESS_ROOT` / `DSH_NODE` / `DSH_STARTUP_TIMEOUT`, and the log location.

### Tests

First tests in the repo, covering the pure functions only — no process spawning:

- `parseWebURL`: the real line, the LAN-suffixed variant, the
  `dsh web: opening the default browser…` line (must not match), and no-match.
- `redactToken`: token present, absent, and a bare URL.
- `lineWriter`: line split across writes, no trailing newline, CRLF, a line
  longer than the buffer.
- `resolveEntrypoint`: `.ts` vs `.js` argv construction, absolute vs relative.
- `FindHarness`: precedence, using `t.TempDir()`.

## Verification

1. `go vet ./... && go test ./... && gofmt -l .` — clean.
2. `./build.sh` from a clean tree (`rm -rf bin/`) — must reach "Done" and
   produce `bin/DeepSeek Harness.app` with a real icon. This is the regression
   test for finding 15.
3. `open 'bin/DeepSeek Harness.app'` — the loading page appears immediately,
   then swaps to the harness UI.
4. **Graceful shutdown (the core fix).** With the app running:
   `pgrep -fl "apps/cli/src/bin.ts"` → note the PID. Cmd+Q, then re-run
   `pgrep` — no match. Check `~/Library/Logs/DeepSeek Harness/app.log` for a
   clean stop with no "force-killing" warning, and confirm no `token=` appears
   in the file.
5. **Crash handling.** With the app running, `kill -9 <dsh pid>` → an error
   dialog appears and the app quits rather than showing a dead WebView.
6. **Startup failure.** Launch with `DSH_HARNESS_ROOT=/nonexistent` → a dialog
   naming the problem, not a silent exit. Then point `DSH_ENTRYPOINT` at a file
   that exits non-zero and confirm the stderr tail reaches the dialog.
7. **Single instance.** `open` the app twice → one window, one server process.
8. **Finder PATH.** Launch from Finder (not a terminal) and confirm node is
   still found — this is what the login-shell probe exists for.
