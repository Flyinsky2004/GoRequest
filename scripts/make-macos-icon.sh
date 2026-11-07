#!/bin/sh
# Rebuild the macOS app icon from the user-provided GoRequest logo.
#
# This is a deterministic technical conversion only: sips resamples the exact
# source PNG to the ten sizes an .iconset needs, and iconutil packs them into
# one .icns. It does NOT redraw, recolor, crop the arrow/trail, add a shadow,
# add a backdrop, or add a second rounded container. The source already has its
# own macOS rounded-square container and a transparent outside; alpha is kept.
#
# Requirements: macOS stock tools only (sips, iconutil). No downloads.
# Run from any directory; paths are relative to the repository root.
#
#   sh scripts/make-macos-icon.sh
#
# Committed outputs (safe to keep): assets/brand/gorequest.icns,
# assets/brand/gorequest-512.png. The temporary .iconset is created in a
# mktemp dir and removed, so no intermediate iconset lands in Git.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SRC="$ROOT/assets/brand/gorequest-logo.png"
ICNS="$ROOT/assets/brand/gorequest.icns"
PNG512="$ROOT/assets/brand/gorequest-512.png"

if [ ! -f "$SRC" ]; then
  echo "missing source: $SRC" >&2
  exit 1
fi
for tool in sips iconutil; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "missing required tool: $tool (macOS only)" >&2
    exit 1
  fi
done

# Guard the source contract: square, 1600x1600, with alpha.
W=$(sips -g pixelWidth "$SRC" | awk '/pixelWidth/{print $2}')
H=$(sips -g pixelHeight "$SRC" | awk '/pixelHeight/{print $2}')
A=$(sips -g hasAlpha "$SRC" | awk '/hasAlpha/{print $2}')
if [ "$W" != 1600 ] || [ "$H" != 1600 ] || [ "$A" != "yes" ]; then
  echo "unexpected source (want 1600x1600 RGBA w/ alpha): ${W}x${H} alpha=$A" >&2
  exit 1
fi

TMP=$(mktemp -d "${TMPDIR:-/tmp}/gorequest-iconset.XXXXXX")
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
ICN="$TMP/gorequest.iconset"
mkdir -p "$ICN"

# name:logical pixels:actual pixels (@2x entries are twice the logical size).
# Each is a plain resample of the same square source, so nothing is cropped.
for spec in \
  "icon_16x16:16" "icon_16x16@2x:32" \
  "icon_32x32:32" "icon_32x32@2x:64" \
  "icon_128x128:128" "icon_128x128@2x:256" \
  "icon_256x256:256" "icon_256x256@2x:512" \
  "icon_512x512:512" "icon_512x512@2x:1024"; do
  name=${spec%%:*}
  px=${spec##*:}
  sips -z "$px" "$px" "$SRC" --out "$ICN/$name.png" >/dev/null
done

# A separate, human-viewable final PNG (useful for README / Linux .desktop).
sips -z 512 512 "$SRC" --out "$PNG512" >/dev/null

iconutil -c icns -o "$ICNS" "$ICN"

echo "wrote $ICNS"
echo "wrote $PNG512"
