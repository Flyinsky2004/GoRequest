#!/bin/sh
# Build a minimal, unsigned, development-only GoRequest.app on macOS.
#
# This wires the generated icon as a real bundle resource (CFBundleIconFile +
# Contents/Resources/gorequest.icns) next to the built executable. It is NOT a
# signed/notarized release or an installer/DMG: it is the smallest reproducible
# .app that lets Finder / the Dock pick up the app icon for local runs.
#
# Requirements: Go, macOS stock tools, and a .icns from make-macos-icon.sh.
#   sh scripts/make-macos-icon.sh
#   sh scripts/make-macos-app.sh [output-dir]     # default: ./bin
#
# The built app is an untracked build artifact (bin/ is gitignored). Nothing
# here touches signing identities, keychains, or the user's real app windows.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUT=${1:-"$ROOT/bin"}
APP="$OUT/GoRequest.app"
ICNS="$ROOT/assets/brand/gorequest.icns"

if [ ! -f "$ICNS" ]; then
  echo "missing $ICNS; run: sh scripts/make-macos-icon.sh" >&2
  exit 1
fi

mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# Resolve a display name / version from the repo, with fixed fallbacks. No
# network, no generated design: only the packaging metadata.
cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>               <string>GoRequest</string>
  <key>CFBundleDisplayName</key>        <string>GoRequest</string>
  <key>CFBundleIdentifier</key>         <string>local.gorequest.desktop</string>
  <key>CFBundleExecutable</key>         <string>gorequest-desktop</string>
  <key>CFBundlePackageType</key>        <string>APPL</string>
  <key>CFBundleIconFile</key>           <string>gorequest.icns</string>
  <key>CFBundleShortVersionString</key> <string>0.1.0</string>
  <key>CFBundleVersion</key>            <string>1</string>
  <key>LSMinimumSystemVersion</key>     <string>11.0</string>
  <key>NSHighResolutionCapable</key>   <true/>
</dict>
</plist>
PLIST

cp "$ICNS" "$APP/Contents/Resources/gorequest.icns"

# Validate the plist with the stock tool; fail loudly on malformed packaging.
plutil -lint "$APP/Contents/Info.plist" >/dev/null

# The build is last: this branch may carry unrelated in-progress compile
# errors from other tasks (e.g. parallel work on diagnostics / Mock), and an
# incomplete bundle should not be left looking finished. Fix the reported
# errors in their owning tasks, then re-run; this script never patches them.
if ! (cd "$ROOT" && go build -o "$APP/Contents/MacOS/gorequest-desktop" ./cmd/gorequest-desktop); then
  echo "go build failed; no runnable executable was produced (bundle metadata/icon are still in place)" >&2
  echo "resolve compile errors in their owning tasks, then re-run this script" >&2
  exit 1
fi

echo "built $APP"
echo "icon:  $APP/Contents/Resources/gorequest.icns (CFBundleIconFile)"
echo "run:   open \"$APP\" --args -project \"$ROOT/examples/sample-project\""
