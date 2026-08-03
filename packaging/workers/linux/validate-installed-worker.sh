#!/bin/sh
set -eu

expected_device_name=""
expect_lan_only=""

usage() {
    echo "Usage: ./validate-installed-worker.sh [--device-name NAME] [--lan-only|--remote-enabled]" >&2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --device-name)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            expected_device_name=$2
            shift 2
            ;;
        --lan-only)
            expect_lan_only=true
            shift
            ;;
        --remote-enabled)
            expect_lan_only=false
            shift
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

if [ "$(uname -s)" != "Linux" ]; then
    echo "Installed worker validation must run on Linux." >&2
    exit 1
fi
if ! command -v systemctl >/dev/null 2>&1; then
    echo "systemctl is required to validate the worker service." >&2
    exit 1
fi

data_home=${XDG_DATA_HOME:-"$HOME/.local/share"}
config_home=${XDG_CONFIG_HOME:-"$HOME/.config"}
install_dir="$data_home/computehop/worker"
service_file="$config_home/systemd/user/computehop-worker.service"
runner="$install_dir/run-installed-worker.sh"
cli="$install_dir/bin/computehop"
daemon="$install_dir/bin/computehopd"

for executable in "$cli" "$daemon" "$install_dir/run-worker.sh" "$runner"; do
    if [ ! -x "$executable" ]; then
        echo "Installed worker file is missing or not executable: $executable" >&2
        exit 1
    fi
done
if [ ! -f "$service_file" ]; then
    echo "systemd user service is missing: $service_file" >&2
    exit 1
fi
if ! grep -q 'Description=ComputeHop worker' "$service_file"; then
    echo "systemd service does not look like ComputeHop's worker service: $service_file" >&2
    exit 1
fi
if ! grep -Fq "$runner" "$service_file"; then
    echo "systemd service does not point at the installed worker runner." >&2
    exit 1
fi
if [ -n "$expected_device_name" ] && ! grep -Fq "COMPUTEHOP_DEVICE_NAME=$expected_device_name" "$service_file"; then
    echo "systemd service does not use expected worker name: $expected_device_name" >&2
    exit 1
fi
if [ "$expect_lan_only" = true ] && ! grep -q -- '--lan-only' "$runner"; then
    echo "Expected installed worker runner to include --lan-only." >&2
    exit 1
fi
if [ "$expect_lan_only" = false ] && grep -q -- '--lan-only' "$runner"; then
    echo "Expected installed worker runner to allow remote connectivity, but --lan-only is present." >&2
    exit 1
fi

systemctl --user is-enabled computehop-worker.service >/dev/null
systemctl --user is-active computehop-worker.service >/dev/null

"$cli" version >/dev/null
status_output=$("$cli" status)
if ! printf '%s\n' "$status_output" | grep -q '(worker,'; then
    echo "Worker daemon status did not report worker role." >&2
    printf '%s\n' "$status_output" >&2
    exit 1
fi
if [ -n "$expected_device_name" ] && ! printf '%s\n' "$status_output" | grep -Fq "Device: $expected_device_name "; then
    echo "Worker daemon status did not report expected device name: $expected_device_name" >&2
    printf '%s\n' "$status_output" >&2
    exit 1
fi
"$cli" doctor >/dev/null

echo "Installed ComputeHop worker validation passed."
echo "Install dir: $install_dir"
echo "Service: $service_file"
