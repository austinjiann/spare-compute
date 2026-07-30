#!/bin/sh
set -eu

targets=${COMPUTEHOP_WORKER_TARGETS:-"linux/amd64 linux/arm64 windows/amd64"}
output_dir=""

usage() {
    echo "Usage: packaging/workers/archive.sh [--output-dir DIR]" >&2
    echo "Set COMPUTEHOP_WORKER_TARGETS='linux/amd64 windows/amd64' to override targets." >&2
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

if [ -z "$output_dir" ]; then
    output_dir="$repository_dir/dist/workers"
else
    case "$output_dir" in
        /*) ;;
        *) output_dir="$repository_dir/$output_dir" ;;
    esac
fi
mkdir -p "$output_dir"

version=${COMPUTEHOP_VERSION:-0.1.0}
if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+(\.[0-9]+){1,2}$'; then
    echo "COMPUTEHOP_VERSION must look like 1.2 or 1.2.3." >&2
    exit 1
fi

staging_dir=$(mktemp -d "${TMPDIR:-/tmp}/computehop-workers.XXXXXX")
cleanup() {
    rm -rf -- "$staging_dir"
}
trap cleanup EXIT HUP INT TERM

write_readme() {
    package_dir=$1
    target_os=$2
    target_arch=$3
    cat >"$package_dir/README.txt" <<EOF
ComputeHop worker package

Target: $target_os/$target_arch
Version: $version

This package is for running a worker computer controlled by a Mac orchestrator.
It is a developer package, not a signed production installer.

Same-LAN quick start:
1. Start the worker on this computer.
2. From the Mac orchestrator, run: computehop connect nearby
3. Compare the pairing code on both devices.
4. Confirm on both devices with: computehop connect confirm
5. From the Mac orchestrator, run: computehop smoke

The worker stores local state in ComputeHop's per-user state directory for this
operating system. Pairings and job history survive restarts.
EOF
}

copy_common_files() {
    package_dir=$1
    target_os=$2
    target_arch=$3
    mkdir -p "$package_dir/bin"
    write_readme "$package_dir" "$target_os" "$target_arch"
}

build_binaries() {
    package_dir=$1
    target_os=$2
    target_arch=$3
    extension=""
    if [ "$target_os" = "windows" ]; then
        extension=".exe"
    fi
    (
        cd "$repository_dir"
        GOOS=$target_os GOARCH=$target_arch go build -trimpath -ldflags "-s -w -X main.version=$version" \
            -o "$package_dir/bin/computehop$extension" ./cmd/computehop
        GOOS=$target_os GOARCH=$target_arch go build -trimpath -ldflags "-s -w -X main.version=$version" \
            -o "$package_dir/bin/computehopd$extension" ./cmd/computehopd
    )
}

archive_zip() {
    package_name=$1
    archive_path=$2
    if command -v ditto >/dev/null 2>&1; then
        (
            cd "$staging_dir"
            COPYFILE_DISABLE=1 ditto -c -k --keepParent --norsrc "$package_name" "$archive_path"
        )
        return
    fi
    if command -v zip >/dev/null 2>&1; then
        (
            cd "$staging_dir"
            zip -qr "$archive_path" "$package_name"
        )
        return
    fi
    echo "Creating Windows worker archives requires ditto or zip." >&2
    exit 1
}

for target in $targets; do
    target_os=${target%/*}
    target_arch=${target#*/}
    case "$target_os/$target_arch" in
        linux/amd64|linux/arm64|windows/amd64) ;;
        *)
            echo "Unsupported worker target: $target" >&2
            echo "Supported targets: linux/amd64 linux/arm64 windows/amd64" >&2
            exit 1
            ;;
    esac

    package_name="ComputeHop-worker-$target_os-$target_arch"
    package_dir="$staging_dir/$package_name"
    mkdir -p "$package_dir"
    copy_common_files "$package_dir" "$target_os" "$target_arch"
    build_binaries "$package_dir" "$target_os" "$target_arch"

    case "$target_os" in
        linux)
            cp "$script_dir/linux/run-worker.sh" "$package_dir/run-worker.sh"
            cp "$script_dir/linux/install-systemd-user.sh" "$package_dir/install-systemd-user.sh"
            chmod +x "$package_dir/run-worker.sh" "$package_dir/install-systemd-user.sh" \
                "$package_dir/bin/computehop" "$package_dir/bin/computehopd"
            archive_name="$package_name.tar.gz"
            archive_path="$output_dir/$archive_name"
            rm -f -- "$archive_path" "$archive_path.sha256"
            (
                cd "$staging_dir"
                tar -czf "$archive_path" "$package_name"
            )
            ;;
        windows)
            cp "$script_dir/windows/run-worker.ps1" "$package_dir/run-worker.ps1"
            cp "$script_dir/windows/install-scheduled-task.ps1" "$package_dir/install-scheduled-task.ps1"
            archive_name="$package_name.zip"
            archive_path="$output_dir/$archive_name"
            rm -f -- "$archive_path" "$archive_path.sha256"
            archive_zip "$package_name" "$archive_path"
            ;;
    esac

    (
        cd "$output_dir"
        if [ "$checksum_command" = "shasum" ]; then
            shasum -a 256 "$archive_name" >"$archive_name.sha256"
        else
            sha256sum "$archive_name" >"$archive_name.sha256"
        fi
    )
    echo "Archived $archive_path"
done
