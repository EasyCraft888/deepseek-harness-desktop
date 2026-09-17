# DeepSeek Harness Desktop

Wails v3 desktop wrapper for [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness).

Launches the harness server directly via `node` and loads it in a native WebView window.

## Prerequisites

- [Go](https://go.dev) 1.25+
- [Node.js](https://nodejs.org) (detected from login-shell PATH or `DSH_NODE` override)
- [deepseek-harness](https://github.com/deepseek-ai/deepseek-harness) checkout at `../deepseek-harness`

## Quick start

```sh
# Build
./build.sh

# Run
open 'bin/DeepSeek Harness.app'
```

For development without packaging:

```sh
go build -ldflags="-X 'main.harnessPath=/path/to/deepseek-harness'" -o bin/deepseek-harness-desktop .
./bin/deepseek-harness-desktop
```

## Environment variables

| Variable | Default | Purpose |
| --- | --- | --- |
| `DSH_HARNESS_ROOT` | (auto-detected) | Path to the deepseek-harness checkout |
| `DSH_ENTRYPOINT` | `apps/cli/src/bin.ts` | Harness entrypoint (relative to harness root, or absolute) |
| `DSH_NODE` | (login-shell PATH) | Path to the Node.js binary |
| `DSH_STARTUP_TIMEOUT` | `60s` | Maximum time to wait for the server URL |

If `DSH_ENTRYPOINT` ends in `.js`, the prebuilt file is used directly (faster start).
For `.ts` entrypoints, the harness is launched through `tsx/esm` with
`TSX_TSCONFIG_PATH` set to the harness root `tsconfig.json` (overridable via the
environment).

## How it works

1. The Go binary launches `node --import tsx/esm apps/cli/src/bin.ts web --no-open --port 0`
   from the harness checkout.
2. It reads the authenticated URL from the server's stdout.
3. A loading page is shown immediately; the WebView swaps to the harness UI
   once the server is ready.
4. Closing the window stops the server gracefully (SIGTERM, 10s wait, then SIGKILL).
5. If the server crashes, an error dialog appears and the app quits.

## Logs

Logs are written to `~/Library/Logs/DeepSeek Harness/app.log` (truncated if
larger than 5 MB). Authentication tokens in URLs are redacted before logging.