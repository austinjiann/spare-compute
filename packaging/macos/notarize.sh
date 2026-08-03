#!/bin/sh
set -eu

app_bundle=""

usage() {
    echo "Usage: packaging/macos/notarize.sh [--app /path/to/ComputeHop.app]" >&2
    echo "Requires either COMPUTEHOP_NOTARY_KEYCHAIN_PROFILE, or" >&2
    echo "COMPUTEHOP_NOTARY_APPLE_ID, COMPUTEHOP_NOTARY_TEAM_ID, and COMPUTEHOP_NOTARY_PASSWORD." >&2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --app)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            app_bundle=$2
            shift 2
            ;;
        *)
            usage
            exit 1
            ;;
    esac
done

if [ "$(uname -s)" != "Darwin" ]; then
    echo "macOS notarization must run on macOS." >&2
    exit 1
fi

for tool in codesign ditto spctl xcrun; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "Required tool is missing: $tool" >&2
        exit 1
    fi
done

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)

if [ -z "$app_bundle" ]; then
    app_bundle="$repository_dir/dist/macos/ComputeHop.app"
else
    case "$app_bundle" in
        /*) ;;
        *) app_bundle="$(CDPATH= cd -- "$(dirname -- "$app_bundle")" && pwd -P)/$(basename -- "$app_bundle")" ;;
    esac
fi

if [ ! -d "$app_bundle" ]; then
    echo "App bundle does not exist: $app_bundle" >&2
    exit 1
fi

"$script_dir/verify.sh" "$app_bundle" >/dev/null
signature=$(/usr/bin/codesign -dv --verbose=4 "$app_bundle" 2>&1 || true)
case "$signature" in
    *"Authority=Developer ID Application"*) ;;
    *)
        echo "App is not signed with a Developer ID Application certificate." >&2
        echo "Build with COMPUTEHOP_CODESIGN_IDENTITY=\"Developer ID Application: ...\" first." >&2
        exit 1
        ;;
esac
case "$signature" in
    *"Runtime Version="*|*"flags="*"runtime"*) ;;
    *)
        echo "App is not signed with hardened runtime enabled." >&2
        exit 1
        ;;
esac

notary_profile=${COMPUTEHOP_NOTARY_KEYCHAIN_PROFILE:-}
notary_apple_id=${COMPUTEHOP_NOTARY_APPLE_ID:-}
notary_team_id=${COMPUTEHOP_NOTARY_TEAM_ID:-}
notary_password=${COMPUTEHOP_NOTARY_PASSWORD:-}
if [ -z "$notary_profile" ] && \
    { [ -z "$notary_apple_id" ] || [ -z "$notary_team_id" ] || [ -z "$notary_password" ]; }; then
    usage
    exit 1
fi

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/computehop-notary.XXXXXX")
cleanup() {
    rm -rf -- "$temporary_dir"
}
trap cleanup EXIT HUP INT TERM

notary_zip="$temporary_dir/ComputeHop-notary.zip"
app_parent=$(dirname -- "$app_bundle")
app_name=$(basename -- "$app_bundle")
(
    cd "$app_parent"
    ditto -c -k --keepParent "$app_name" "$notary_zip"
)

if [ -n "$notary_profile" ]; then
    xcrun notarytool submit "$notary_zip" --keychain-profile "$notary_profile" --wait
else
    xcrun notarytool submit "$notary_zip" \
        --apple-id "$notary_apple_id" \
        --team-id "$notary_team_id" \
        --password "$notary_password" \
        --wait
fi

xcrun stapler staple "$app_bundle"
xcrun stapler validate "$app_bundle" >/dev/null
spctl --assess --type execute --verbose=4 "$app_bundle" >/dev/null

echo "Notarized and stapled $app_bundle"
