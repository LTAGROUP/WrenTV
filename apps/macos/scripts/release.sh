#!/bin/bash
# Builds a notarized WrenTV-macOS.zip that runs without Homebrew.
#
#   scripts/release.sh [output dir]
#
# Needs: Homebrew mpv (it gets copied into the app), a "Developer ID
# Application" certificate in the keychain, and notarytool credentials saved
# under the profile below (xcrun notarytool store-credentials wrentv-notary).
set -euo pipefail

cd "$(dirname "$0")/.."
OUT="${1:-build/release}"
IDENTITY="${IDENTITY:-Developer ID Application}"
PROFILE="${NOTARY_PROFILE:-wrentv-notary}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

xcodebuild -project WrenTV.xcodeproj -scheme WrenTV -configuration Release \
  -destination 'platform=macOS,arch=arm64' -derivedDataPath "$WORK/dd" -quiet build
APP="$WORK/Wren TV.app"
cp -R "$WORK/dd/Build/Products/Release/Wren TV.app" "$APP"

python3 scripts/bundle_dylibs.py "$APP" 2> >(grep -v 'invalidate the code signature' >&2)

# Inside out: libraries first, then the app with the hardened runtime.
for lib in "$APP"/Contents/Frameworks/*.dylib; do
  codesign --force --timestamp --options runtime --sign "$IDENTITY" "$lib"
done
codesign --force --timestamp --options runtime --entitlements scripts/release.entitlements \
  --sign "$IDENTITY" "$APP"
codesign --verify --deep --strict "$APP"

ditto -c -k --keepParent "$APP" "$WORK/notarize.zip"
xcrun notarytool submit "$WORK/notarize.zip" --keychain-profile "$PROFILE" --wait
xcrun stapler staple "$APP"
spctl --assess --type execute --verbose "$APP"

mkdir -p "$OUT"
rm -f "$OUT/WrenTV-macOS.zip"
ditto -c -k --keepParent "$APP" "$OUT/WrenTV-macOS.zip"
echo "$OUT/WrenTV-macOS.zip"
