#!/bin/sh
set -eu

if [ "$(uname -s)" != "Darwin" ]; then
    echo "macOS bundle verification must run on macOS." >&2
    exit 1
fi

app_bundle=${1:-}
if [ -z "$app_bundle" ] || [ ! -d "$app_bundle" ]; then
    echo "Usage: packaging/macos/verify.sh /path/to/ComputeHop.app" >&2
    exit 1
fi

info_plist="$app_bundle/Contents/Info.plist"
app_executable="$app_bundle/Contents/MacOS/ComputeHop"
cli_executable="$app_bundle/Contents/Resources/bin/computehop"
daemon_executable="$app_bundle/Contents/Resources/bin/computehopd"

plutil -lint "$info_plist" >/dev/null
bundle_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$info_plist")
if [ "$bundle_id" != "com.computehop.app" ]; then
    echo "Unexpected bundle identifier: $bundle_id" >&2
    exit 1
fi
for executable in "$app_executable" "$cli_executable" "$daemon_executable"; do
    if [ ! -x "$executable" ]; then
        echo "Bundle executable is missing: $executable" >&2
        exit 1
    fi
done

codesign --verify --deep --strict "$app_bundle"
"$cli_executable" version >/dev/null
"$daemon_executable" --version >/dev/null
echo "Verified $app_bundle"
