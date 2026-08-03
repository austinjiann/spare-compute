#!/bin/sh
set -eu

archive_name=${COMPUTEHOP_ARCHIVE_NAME:-ComputeHop-macos.zip}
app_source=""
output_dir=""

usage() {
    echo "Usage: packaging/macos/archive.sh [--app /path/to/ComputeHop.app] [--output-dir DIR]" >&2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --app)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            app_source=$2
            shift 2
            ;;
        --output-dir)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            output_dir=$2
            shift 2
            ;;
        *)
            usage
            exit 1
            ;;
    esac
done

case "$archive_name" in
    *.zip) ;;
    *) echo "COMPUTEHOP_ARCHIVE_NAME must end in .zip." >&2; exit 1 ;;
esac

if [ "$(uname -s)" != "Darwin" ]; then
    echo "macOS archive creation must run on macOS." >&2
    exit 1
fi

for tool in codesign ditto shasum plutil; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "Required tool is missing: $tool" >&2
        exit 1
    fi
done

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)

if [ -z "$output_dir" ]; then
    output_dir="$repository_dir/dist/macos"
else
    case "$output_dir" in
        /*) ;;
        *) output_dir="$repository_dir/$output_dir" ;;
    esac
fi
mkdir -p "$output_dir"

if [ -n "$app_source" ]; then
    case "$app_source" in
        /*) ;;
        *) app_source="$(CDPATH= cd -- "$(dirname -- "$app_source")" && pwd -P)/$(basename -- "$app_source")" ;;
    esac
    if [ ! -d "$app_source" ]; then
        echo "--app must point at an existing ComputeHop.app bundle." >&2
        exit 1
    fi
    "$script_dir/verify.sh" "$app_source" >/dev/null
    built_app="$app_source"
else
    "$script_dir/build.sh" "$output_dir"
    built_app="$output_dir/ComputeHop.app"
fi

version=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' \
    "$built_app/Contents/Info.plist" 2>/dev/null || true)
build_number=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' \
    "$built_app/Contents/Info.plist" 2>/dev/null || true)
machine_arch=$(uname -m)
payload_name="ComputeHop-macos"
archive_path="$output_dir/$archive_name"
checksum_path="$archive_path.sha256"
staging_dir=$(mktemp -d "${TMPDIR:-/tmp}/computehop-archive.XXXXXX")
verify_dir=$(mktemp -d "${TMPDIR:-/tmp}/computehop-archive-verify.XXXXXX")

cleanup() {
    rm -rf -- "$staging_dir" "$verify_dir"
}
trap cleanup EXIT HUP INT TERM

payload_dir="$staging_dir/$payload_name"
mkdir -p "$payload_dir"
ditto "$built_app" "$payload_dir/ComputeHop.app"
for support_file in \
    install.sh \
    uninstall.sh \
    validate-installed.sh \
    verify.sh \
    verify-control-center-background.js \
    com.computehop.daemon.plist \
    entitlements.plist; do
    cp "$script_dir/$support_file" "$payload_dir/$support_file"
done

signature=$(/usr/bin/codesign -dv --verbose=4 "$built_app" 2>&1 || true)
notarized=false
if command -v xcrun >/dev/null 2>&1 && \
    printf '%s\n' "$signature" | grep -q 'Authority=Developer ID Application' && \
    xcrun stapler validate "$built_app" >/dev/null 2>&1; then
    notarized=true
fi

if [ "$notarized" = true ]; then
    cat >"$payload_dir/README.txt" <<EOF
ComputeHop macOS signed package

This archive is signed with Developer ID and has a stapled notarization ticket.
Run clean-machine validation before publishing it as a public release.

Built version: ${version:-unknown}
Build number: ${build_number:-unknown}
Built architecture: $machine_arch

Install as the control Mac:
  ./install.sh --role orchestrator

Install as a worker Mac:
  ./install.sh --role worker --device-name "Gaming PC" --lan-only

Check without changing the Mac first:
  ./install.sh --check --role worker --device-name "Gaming PC" --lan-only

Check uninstall without changing the Mac:
  ./uninstall.sh --check
EOF
else
    cat >"$payload_dir/README.txt" <<EOF
ComputeHop macOS developer package

This archive is for local two-Mac testing. It is ad-hoc signed, not notarized,
and not a public release artifact.

Built version: ${version:-unknown}
Build number: ${build_number:-unknown}
Built architecture: $machine_arch

Install as the control Mac:
  ./install.sh --role orchestrator

Install as a worker Mac:
  ./install.sh --role worker --device-name "Gaming PC" --lan-only

Check without changing the Mac first:
  ./install.sh --check --role worker --device-name "Gaming PC" --lan-only

Check uninstall without changing the Mac:
  ./uninstall.sh --check
EOF
fi

rm -f -- "$archive_path" "$checksum_path"
(
    cd "$staging_dir"
    ditto -c -k --keepParent "$payload_name" "$archive_path"
)
(
    cd "$output_dir"
    shasum -a 256 "$archive_name" >"$(basename -- "$checksum_path")"
)

ditto -x -k "$archive_path" "$verify_dir"
"$verify_dir/$payload_name/verify.sh" "$verify_dir/$payload_name/ComputeHop.app" >/dev/null

echo "Archived $archive_path"
echo "Checksum $checksum_path"
