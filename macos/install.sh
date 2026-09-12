#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR="$HOME/Applications/Pixel Fleet.app"
BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pixel-fleet-app.XXXXXX")"
cleanup() {
    if [ ! -d "$APP_DIR" ] && [ -d "$BUILD_DIR/previous.app" ]; then
        mv "$BUILD_DIR/previous.app" "$APP_DIR"
    fi
    rm -rf "$BUILD_DIR"
}
trap cleanup EXIT
BUNDLE="$BUILD_DIR/Pixel Fleet.app"
mkdir -p "$BUNDLE/Contents/MacOS" "$BUNDLE/Contents/Resources" "$BUILD_DIR/PixelFleet.iconset"
cd "$ROOT"
go build -o "$BUNDLE/Contents/MacOS/cs" .
swiftc -swift-version 5 -target "$(uname -m)-apple-macosx13.0" macos/PixelFleet.swift -o "$BUNDLE/Contents/MacOS/PixelFleet" -framework AppKit -framework UserNotifications -framework ServiceManagement
cp macos/Info.plist "$BUNDLE/Contents/Info.plist"
swift macos/Icon.swift "$BUILD_DIR/icon.png"
for size in 16 32 128 256 512; do
    sips -z "$size" "$size" "$BUILD_DIR/icon.png" --out "$BUILD_DIR/PixelFleet.iconset/icon_${size}x${size}.png" >/dev/null
    doubled=$((size * 2))
    sips -z "$doubled" "$doubled" "$BUILD_DIR/icon.png" --out "$BUILD_DIR/PixelFleet.iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$BUILD_DIR/PixelFleet.iconset" -o "$BUNDLE/Contents/Resources/PixelFleet.icns"
codesign --force --sign - --identifier com.pixelfleet.cli "$BUNDLE/Contents/MacOS/cs"
# Keep the local app identity stable across rebuilds instead of using a
# designated requirement tied to each executable's changing cdhash.
codesign --force --sign - --requirements '=designated => identifier "com.pixelfleet.desktop"' "$BUNDLE"
codesign --verify --deep --strict "$BUNDLE"
mkdir -p "$HOME/Applications" "$HOME/.local/bin"
# Stop this app before replacing its bundle. Do not terminate agent sessions.
for pid in $(pgrep -x PixelFleet || true); do
    executable=$(ps -p "$pid" -o comm=)
    if [ "$executable" = "$APP_DIR/Contents/MacOS/PixelFleet" ]; then kill -TERM "$pid"; fi
done
# Keep an installed bundle as a rollback copy until the new copy is in place.
if [ -e "$APP_DIR" ]; then mv "$APP_DIR" "$BUILD_DIR/previous.app"; fi
if ! ditto "$BUNDLE" "$APP_DIR"; then
    rm -rf "$APP_DIR"
    exit 1
fi
cp "$BUNDLE/Contents/MacOS/cs" "$HOME/.local/bin/.cs-app-new"
chmod 755 "$HOME/.local/bin/.cs-app-new"
mv "$HOME/.local/bin/.cs-app-new" "$HOME/.local/bin/cs"
# Retire the previous watcher so the app starts one with the new protocol.
for pid in $(pgrep -f 'cs --notify-worker' || true); do
    command=$(ps -p "$pid" -o command=)
    case "$command" in
        "$HOME/.local/bin/cs --notify-worker "*|"$APP_DIR/Contents/MacOS/cs --notify-worker "*) kill -TERM "$pid" ;;
    esac
done
open -g "$APP_DIR"
printf 'Installed %s and ~/.local/bin/cs\n' "$APP_DIR"
printf 'Allow Pixel Fleet notifications when macOS asks. Launch at login is optional in its menu.\n'
