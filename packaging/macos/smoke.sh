#!/bin/sh
set -eu

archive_name=${COMPUTEHOP_ARCHIVE_NAME:-ComputeHop-macos-smoke.zip}
app_source=""
output_dir=""
cleanup_output=false

usage() {
    echo "Usage: packaging/macos/smoke.sh [--app /path/to/ComputeHop.app] [--output-dir DIR]" >&2
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
    echo "macOS archive smoke testing must run on macOS." >&2
    exit 1
fi

for tool in ditto shasum; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "Required tool is missing: $tool" >&2
        exit 1
    fi
done

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)
workspace_dir=$(mktemp -d "${TMPDIR:-/tmp}/computehop-macos-smoke.XXXXXX")

cleanup() {
    rm -rf -- "$workspace_dir"
    if [ "$cleanup_output" = true ] && [ -n "$output_dir" ]; then
        rm -rf -- "$output_dir"
    fi
}
trap cleanup EXIT HUP INT TERM

if [ -z "$output_dir" ]; then
    output_dir=$(mktemp -d "${TMPDIR:-/tmp}/computehop-macos-archive.XXXXXX")
    cleanup_output=true
else
    case "$output_dir" in
        /*) ;;
        *) output_dir="$repository_dir/$output_dir" ;;
    esac
fi
mkdir -p "$output_dir"

if [ -n "$app_source" ]; then
    COMPUTEHOP_ARCHIVE_NAME="$archive_name" "$script_dir/archive.sh" \
        --app "$app_source" \
        --output-dir "$output_dir" >/dev/null
else
    COMPUTEHOP_ARCHIVE_NAME="$archive_name" "$script_dir/archive.sh" \
        --output-dir "$output_dir" >/dev/null
fi

archive_path="$output_dir/$archive_name"
checksum_path="$archive_path.sha256"
if [ ! -f "$archive_path" ]; then
    echo "Archive was not created: $archive_path" >&2
    exit 1
fi
if [ ! -f "$checksum_path" ]; then
    echo "Archive checksum was not created: $checksum_path" >&2
    exit 1
fi

(
    cd "$output_dir"
    shasum -a 256 -c "$(basename -- "$checksum_path")" >/dev/null
)

extract_dir="$workspace_dir/extracted"
mkdir -p "$extract_dir"
ditto -x -k "$archive_path" "$extract_dir"
package_root="$extract_dir/ComputeHop-macos"
if [ ! -d "$package_root/ComputeHop.app" ]; then
    echo "Archive did not contain ComputeHop.app at the expected path." >&2
    exit 1
fi
for support_file in README.txt install.sh validate-installed.sh verify.sh verify-control-center-background.js com.computehop.daemon.plist; do
    if [ ! -f "$package_root/$support_file" ]; then
        echo "Archive support file is missing: $support_file" >&2
        exit 1
    fi
done
if [ ! -f "$package_root/entitlements.plist" ]; then
    echo "Archive support file is missing: entitlements.plist" >&2
    exit 1
fi

"$package_root/verify.sh" "$package_root/ComputeHop.app" >/dev/null
"$package_root/ComputeHop.app/Contents/Resources/bin/computehop" version >/dev/null
"$package_root/ComputeHop.app/Contents/Resources/bin/computehopd" --version >/dev/null
"$package_root/ComputeHop.app/Contents/Resources/ComputeHop Control Center.app/Contents/Resources/bin/computehopd" --version >/dev/null

orchestrator_home="$workspace_dir/orchestrator-home"
worker_home="$workspace_dir/worker-home"
mkdir -p "$orchestrator_home" "$worker_home"

orchestrator_check=$(HOME="$orchestrator_home" "$package_root/install.sh" \
    --check \
    --role orchestrator \
    --lan-only 2>&1)
printf '%s\n' "$orchestrator_check" | grep -q "Install check passed."
printf '%s\n' "$orchestrator_check" | grep -q "Would run daemon as: orchestrator"
printf '%s\n' "$orchestrator_check" | grep -q "Would install in LAN-only mode."

worker_check=$(HOME="$worker_home" "$package_root/install.sh" \
    --check \
    --role worker \
    --device-name "Smoke Worker" \
    --cache-size 1GiB \
    --lan-only 2>&1)
printf '%s\n' "$worker_check" | grep -q "Install check passed."
printf '%s\n' "$worker_check" | grep -q "Would run daemon as: worker"
printf '%s\n' "$worker_check" | grep -q "Would use device name: Smoke Worker"
printf '%s\n' "$worker_check" | grep -q "Would set cache size: 1GiB"
printf '%s\n' "$worker_check" | grep -q "Would install in LAN-only mode."

echo "macOS archive smoke passed: $archive_path"
