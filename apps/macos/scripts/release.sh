#!/bin/bash
# Builds a notarized WrenTV-macOS.dmg (open it, drag Wren TV to
# Applications), the same app as WrenTV-macOS.zip, and appcast.xml, the
# feed the app checks for updates. All three go on the GitHub release
# tagged v<version>.
#
#   scripts/release.sh [output dir]
#
# Needs: Homebrew mpv (it gets copied into the app), a "Developer ID
# Application" certificate in the keychain, notarytool credentials saved
# under the profile below (xcrun notarytool store-credentials wrentv-notary),
# and the Sparkle update key in the keychain (generate_keys --account wrentv).
set -euo pipefail

cd "$(dirname "$0")/.."
OUT="${1:-build/release}"
IDENTITY="${IDENTITY:-Developer ID Application}"
PROFILE="${NOTARY_PROFILE:-wrentv-notary}"
REPO="https://github.com/LTAGROUP/WrenTV"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

xcodebuild -project WrenTV.xcodeproj -scheme WrenTV -configuration Release \
  -destination 'platform=macOS,arch=arm64' -derivedDataPath "$WORK/dd" -quiet build
APP="$WORK/Wren TV.app"
cp -R "$WORK/dd/Build/Products/Release/Wren TV.app" "$APP"

python3 scripts/bundle_dylibs.py "$APP" 2> >(grep -v 'invalidate the code signature' >&2)

# Inside out: libraries and Sparkle's helpers first, then the app with the
# hardened runtime.
for lib in "$APP"/Contents/Frameworks/*.dylib; do
  codesign --force --timestamp --options runtime --sign "$IDENTITY" "$lib"
done
SPK="$APP/Contents/Frameworks/Sparkle.framework"
for part in XPCServices/Installer.xpc XPCServices/Downloader.xpc Autoupdate Updater.app; do
  codesign --force --timestamp --options runtime --preserve-metadata=entitlements \
    --sign "$IDENTITY" "$SPK/Versions/B/$part"
done
codesign --force --timestamp --options runtime --sign "$IDENTITY" "$SPK"
codesign --force --timestamp --options runtime --entitlements scripts/release.entitlements \
  --sign "$IDENTITY" "$APP"
codesign --verify --deep --strict "$APP"

ditto -c -k --keepParent "$APP" "$WORK/notarize.zip"
xcrun notarytool submit "$WORK/notarize.zip" --keychain-profile "$PROFILE" --wait
xcrun stapler staple "$APP"
spctl --assess --type execute --verbose "$APP"

mkdir -p "$OUT"
rm -f "$OUT/WrenTV-macOS.zip" "$OUT/WrenTV-macOS.dmg" "$OUT/appcast.xml"
ditto -c -k --keepParent "$APP" "$OUT/WrenTV-macOS.zip"

# The disk image: the app beside a link to Applications.
mkdir "$WORK/dmg"
cp -R "$APP" "$WORK/dmg/"
ln -s /Applications "$WORK/dmg/Applications"
hdiutil create -volname "Wren TV" -srcfolder "$WORK/dmg" -fs HFS+ -format UDZO -ov "$OUT/WrenTV-macOS.dmg" >/dev/null
codesign --force --timestamp --sign "$IDENTITY" "$OUT/WrenTV-macOS.dmg"
xcrun notarytool submit "$OUT/WrenTV-macOS.dmg" --keychain-profile "$PROFILE" --wait
xcrun stapler staple "$OUT/WrenTV-macOS.dmg"
spctl --assess --type open --context context:primary-signature --verbose "$OUT/WrenTV-macOS.dmg"

# The update feed lists this version; the app finds it on the latest
# release and downloads the disk image from this one.
PLIST="$APP/Contents/Info.plist"
VERSION=$(/usr/libexec/PlistBuddy -c "Print :CFBundleShortVersionString" "$PLIST")
BUILD=$(/usr/libexec/PlistBuddy -c "Print :CFBundleVersion" "$PLIST")
MIN_OS=$(/usr/libexec/PlistBuddy -c "Print :LSMinimumSystemVersion" "$PLIST")
SIGNED=$("$WORK/dd/SourcePackages/artifacts/sparkle/Sparkle/bin/sign_update" --account wrentv "$OUT/WrenTV-macOS.dmg")
cat > "$OUT/appcast.xml" <<XML
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <title>Wren TV</title>
    <item>
      <title>Wren TV $VERSION</title>
      <pubDate>$(LC_ALL=C date -u "+%a, %d %b %Y %H:%M:%S +0000")</pubDate>
      <sparkle:version>$BUILD</sparkle:version>
      <sparkle:shortVersionString>$VERSION</sparkle:shortVersionString>
      <sparkle:minimumSystemVersion>$MIN_OS</sparkle:minimumSystemVersion>
      <link>$REPO/releases/tag/v$VERSION</link>
      <enclosure url="$REPO/releases/download/v$VERSION/WrenTV-macOS.dmg" $SIGNED type="application/octet-stream"/>
    </item>
  </channel>
</rss>
XML
echo "$OUT: WrenTV-macOS.dmg WrenTV-macOS.zip appcast.xml for v$VERSION"
