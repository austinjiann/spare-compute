#!/bin/sh
set -eu

targets=${COMPUTEHOP_WORKER_TARGETS:-"linux/amd64 linux/arm64 windows/amd64"}
archive_dir=""

usage() {
    echo "Usage: packaging/workers/verify.sh [--archive-dir DIR]" >&2
    echo "Set COMPUTEHOP_WORKER_TARGETS='linux/amd64 windows/amd64' to verify a subset." >&2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --archive-dir)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            archive_dir=$2
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            usage
            exit 1
            ;;
    esac
done

for tool in go tar; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "Required tool is missing: $tool" >&2
        exit 1
    fi
done
if command -v shasum >/dev/null 2>&1; then
    checksum_command=shasum
elif command -v sha256sum >/dev/null 2>&1; then
    checksum_command=sha256sum
else
    echo "Required tool is missing: shasum or sha256sum" >&2
    exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)

if [ -z "$archive_dir" ]; then
    archive_dir="$repository_dir/dist/workers"
else
    case "$archive_dir" in
        /*) ;;
        *) archive_dir="$repository_dir/$archive_dir" ;;
    esac
fi

if [ ! -d "$archive_dir" ]; then
    echo "Worker archive directory does not exist: $archive_dir" >&2
    exit 1
fi

workspace_dir=$(mktemp -d "${TMPDIR:-/tmp}/computehop-workers-verify.XXXXXX")
cleanup() {
    rm -rf -- "$workspace_dir"
}
trap cleanup EXIT HUP INT TERM

verify_checksum() {
    archive_name=$1
    archive_path="$archive_dir/$archive_name"
    checksum_path="$archive_path.sha256"

    if [ ! -f "$archive_path" ]; then
        echo "Missing worker archive: $archive_path" >&2
        exit 1
    fi
    if [ ! -f "$checksum_path" ]; then
        echo "Missing worker archive checksum: $checksum_path" >&2
        exit 1
    fi

    (
        cd "$archive_dir"
        if [ "$checksum_command" = "shasum" ]; then
            shasum -a 256 -c "$archive_name.sha256" >/dev/null
        else
            sha256sum -c "$archive_name.sha256" >/dev/null
        fi
    )
}

verify_present() {
    path=$1
    if [ ! -e "$path" ]; then
        echo "Missing packaged file: $path" >&2
        exit 1
    fi
}

verify_executable() {
    path=$1
    verify_present "$path"
    if [ ! -x "$path" ]; then
        echo "Packaged file is not executable: $path" >&2
        exit 1
    fi
}

verify_go_binary() {
    path=$1
    verify_present "$path"
    if ! go version -m "$path" >/dev/null 2>&1; then
        echo "Packaged binary is not an inspectable Go binary: $path" >&2
        exit 1
    fi
}

verify_readme() {
    path=$1
    target=$2
    verify_present "$path"
    if ! grep -q "Target: $target" "$path"; then
        echo "Packaged README is missing target marker '$target': $path" >&2
        exit 1
    fi
}

verify_file_mentions() {
    path=$1
    expected=$2
    verify_present "$path"
    if ! grep -q "$expected" "$path"; then
        echo "Packaged file is missing expected text '$expected': $path" >&2
        exit 1
    fi
}

verify_no_macos_sidecars() {
    search_root=$1
    if find "$search_root" \( -name "__MACOSX" -o -name "._*" \) | grep -q .; then
        echo "Windows worker archive contains macOS sidecar files." >&2
        exit 1
    fi
}

verify_linux_archive() {
    target_arch=$1
    package_name="ComputeHop-worker-linux-$target_arch"
    archive_name="$package_name.tar.gz"
    extract_dir="$workspace_dir/$package_name"
    root="$extract_dir/$package_name"

    verify_checksum "$archive_name"
    mkdir -p "$extract_dir"
    tar -xzf "$archive_dir/$archive_name" -C "$extract_dir"

    verify_readme "$root/README.txt" "linux/$target_arch"
    verify_executable "$root/bin/computehop"
    verify_executable "$root/bin/computehopd"
    verify_executable "$root/run-worker.sh"
    verify_executable "$root/install-systemd-user.sh"
    verify_executable "$root/validate-installed-worker.sh"
    verify_file_mentions "$root/install-systemd-user.sh" "Worker install check passed"
    verify_file_mentions "$root/validate-installed-worker.sh" "Installed ComputeHop worker validation passed"
    verify_go_binary "$root/bin/computehop"
    verify_go_binary "$root/bin/computehopd"
}

verify_windows_archive() {
    target_arch=$1
    package_name="ComputeHop-worker-windows-$target_arch"
    archive_name="$package_name.zip"
    extract_dir="$workspace_dir/$package_name"
    root="$extract_dir/$package_name"

    if ! command -v unzip >/dev/null 2>&1; then
        echo "Required tool is missing for Windows archive verification: unzip" >&2
        exit 1
    fi

    verify_checksum "$archive_name"
    mkdir -p "$extract_dir"
    unzip -q "$archive_dir/$archive_name" -d "$extract_dir"

    verify_no_macos_sidecars "$extract_dir"
    verify_readme "$root/README.txt" "windows/$target_arch"
    verify_present "$root/bin/computehop.exe"
    verify_present "$root/bin/computehopd.exe"
    verify_present "$root/run-worker.ps1"
    verify_present "$root/install-scheduled-task.ps1"
    verify_present "$root/validate-installed-worker.ps1"
    verify_file_mentions "$root/install-scheduled-task.ps1" "Worker install check passed"
    verify_file_mentions "$root/validate-installed-worker.ps1" "Installed ComputeHop worker validation passed"
    verify_go_binary "$root/bin/computehop.exe"
    verify_go_binary "$root/bin/computehopd.exe"
}

for target in $targets; do
    target_os=${target%/*}
    target_arch=${target#*/}
    case "$target_os/$target_arch" in
        linux/amd64|linux/arm64)
            verify_linux_archive "$target_arch"
            ;;
        windows/amd64)
            verify_windows_archive "$target_arch"
            ;;
        *)
            echo "Unsupported worker target: $target" >&2
            echo "Supported targets: linux/amd64 linux/arm64 windows/amd64" >&2
            exit 1
            ;;
    esac
done

echo "Verified worker archives in $archive_dir"
