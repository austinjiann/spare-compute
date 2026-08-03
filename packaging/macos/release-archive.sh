#!/bin/sh
set -eu

output_dir=""

usage() {
    echo "Usage: packaging/macos/release-archive.sh [--output-dir DIR]" >&2
    echo "Requires COMPUTEHOP_CODESIGN_IDENTITY and notarization credentials." >&2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
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

if [ "$(uname -s)" != "Darwin" ]; then
    echo "macOS release archive creation must run on macOS." >&2
    exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)

if [ -z "${COMPUTEHOP_CODESIGN_IDENTITY:-}" ] || [ "${COMPUTEHOP_CODESIGN_IDENTITY:-}" = "-" ]; then
    echo "COMPUTEHOP_CODESIGN_IDENTITY must name a Developer ID Application certificate." >&2
    exit 1
fi

if [ -z "$output_dir" ]; then
    output_dir="$repository_dir/dist/macos"
else
    case "$output_dir" in
        /*) ;;
        *) output_dir="$repository_dir/$output_dir" ;;
    esac
fi

version=${COMPUTEHOP_VERSION:-$(tr -d '\r\n' < "$repository_dir/VERSION")}
machine_arch=$(uname -m)
archive_name=${COMPUTEHOP_ARCHIVE_NAME:-ComputeHop-macos-$version-$machine_arch-notarized.zip}
export COMPUTEHOP_ARCHIVE_NAME="$archive_name"

"$script_dir/build.sh" "$output_dir"
"$script_dir/notarize.sh" --app "$output_dir/ComputeHop.app"
COMPUTEHOP_VERIFY_DEVELOPER_ID=true COMPUTEHOP_VERIFY_NOTARIZED=true \
    "$script_dir/verify.sh" "$output_dir/ComputeHop.app" >/dev/null
"$script_dir/archive.sh" --app "$output_dir/ComputeHop.app" --output-dir "$output_dir"

echo "Release archive ready: $output_dir/$archive_name"
