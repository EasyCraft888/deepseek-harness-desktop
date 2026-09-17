#!/bin/bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR"

HARNESS_ROOT="$(cd "$PROJECT_DIR/../deepseek-harness" && pwd)"
SVG="$PROJECT_DIR/icon.svg"
APP_DIR="bin/DeepSeek Harness.app"
ICONSET="bin/icon.iconset"
ICNS="bin/icon.icns"

echo "=== 1/3: Building Go binary ==="
go build -ldflags="-X 'main.harnessPath=$HARNESS_ROOT'" -o bin/deepseek-harness-desktop .
echo "  -> harness path baked in: $HARNESS_ROOT"

echo "=== 2/3: Generating app icon ==="
rm -rf "$ICONSET" "$ICNS"
mkdir -p "$ICONSET"

# Render SVG to 1024x1024 PNG with dark background for visibility.
# The favicon SVG uses @media (prefers-color-scheme: dark) so on a
# dark-background system it renders as white — perfect for the app.
qlmanage -t -s 1024 -o "$ICONSET" "$SVG" 2>/dev/null || {
  echo "ERROR: qlmanage failed to render SVG."
  echo "Install librsvg and re-run: brew install librsvg"
  exit 1
}

SRC="$ICONSET/favicon.svg.png"
if [ ! -f "$SRC" ]; then
  echo "ERROR: qlmanage did not produce output."
  exit 1
fi

# Generate all required sizes for the iconset.
sips -z 16 16   "$SRC" --out "$ICONSET/icon_16x16.png"       2>/dev/null
sips -z 32 32   "$SRC" --out "$ICONSET/icon_16x16@2x.png"    2>/dev/null
sips -z 32 32   "$SRC" --out "$ICONSET/icon_32x32.png"       2>/dev/null
sips -z 64 64   "$SRC" --out "$ICONSET/icon_32x32@2x.png"    2>/dev/null
sips -z 128 128 "$SRC" --out "$ICONSET/icon_128x128.png"     2>/dev/null
sips -z 256 256 "$SRC" --out "$ICONSET/icon_128x128@2x.png"  2>/dev/null
sips -z 256 256 "$SRC" --out "$ICONSET/icon_256x256.png"     2>/dev/null
sips -z 512 512 "$SRC" --out "$ICONSET/icon_256x256@2x.png"  2>/dev/null
sips -z 512 512 "$SRC" --out "$ICONSET/icon_512x512.png"     2>/dev/null
cp "$SRC"         "$ICONSET/icon_512x512@2x.png"

iconutil -c icns "$ICONSET" -o "$ICNS"
echo "  -> $ICNS ($(du -h "$ICNS" | cut -f1))"

echo "=== 3/3: Packaging .app bundle ==="
rm -rf "$APP_DIR"
mkdir -p "$APP_DIR/Contents/MacOS"
mkdir -p "$APP_DIR/Contents/Resources"

cp bin/deepseek-harness-desktop "$APP_DIR/Contents/MacOS/DeepSeek Harness"
chmod +x "$APP_DIR/Contents/MacOS/DeepSeek Harness"
cp "$ICNS" "$APP_DIR/Contents/Resources/icon.icns"

cat > "$APP_DIR/Contents/Info.plist" << 'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>DeepSeek Harness</string>
	<key>CFBundleIconFile</key>
	<string>icon</string>
	<key>CFBundleIdentifier</key>
	<string>com.deepseek.harness</string>
	<key>CFBundleName</key>
	<string>DeepSeek Harness</string>
	<key>CFBundleDisplayName</key>
	<string>DeepSeek Harness</string>
	<key>CFBundleVersion</key>
	<string>0.1.0</string>
	<key>CFBundleShortVersionString</key>
	<string>0.1.0</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleSignature</key>
	<string>????</string>
	<key>LSMinimumSystemVersion</key>
	<string>13.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
PLIST

echo ""
echo "Done: $APP_DIR"
echo ""
echo "To run in place:"
echo "  open '$APP_DIR'"
echo ""
echo "To install in /Applications:"
echo "  cp -R '$APP_DIR' /Applications/"
echo "  open '/Applications/DeepSeek Harness.app'"