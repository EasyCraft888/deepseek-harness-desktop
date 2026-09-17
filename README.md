# DeepSeek Harness Desktop

Wails v3 desktop wrapper for [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness).

Starts `dsh web` in the background and loads it in a native WebView window.

## Prerequisites

- [Go](https://go.dev) 1.22+
- [Wails v3](https://v3.wails.io) CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@latest`
- [pnpm](https://pnpm.io) (for the harness source launch)
- [deepseek-harness](https://github.com/deepseek-ai/deepseek-harness) checkout at `../deepseek-harness`

## Quick start

```sh
# Build
go build -o bin/deepseek-harness-desktop .

# Run
./bin/deepseek-harness-desktop
```

## How it works

1. The Go binary starts `pnpm run dsh web --no-open` from the neighbouring
   `deepseek-harness` checkout.
2. It reads the authenticated URL from the DSH process stdout.
3. A native WebView window opens and loads that URL.
4. Closing the window stops the DSH process.