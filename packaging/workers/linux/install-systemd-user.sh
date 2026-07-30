#!/bin/sh
set -eu

check_only=false
parsed_arguments=""

usage() {
    echo "Usage: ./install-systemd-user.sh [--check] [daemon flags...]" >&2
    echo "Example: COMPUTEHOP_DEVICE_NAME=\"Gaming PC\" ./install-systemd-user.sh --check --lan-only" >&2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --check)
            check_only=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            case "$1" in
                *'
'*) echo "Daemon arguments must not contain newlines." >&2; exit 1 ;;
            esac
            parsed_arguments="${parsed_arguments}${parsed_arguments:+
}$1"
            shift
            ;;
    esac
done

set --
if [ -n "$parsed_arguments" ]; then
    old_ifs=$IFS
    IFS='
'
    for argument in $parsed_arguments; do
        set -- "$@" "$argument"
    done
    IFS=$old_ifs
fi

if [ "$(uname -s)" != "Linux" ]; then
    echo "The systemd user installer must run on Linux." >&2
    exit 1
fi
if ! command -v systemctl >/dev/null 2>&1; then
    echo "systemctl is required for the user-service installer." >&2
    exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
device_name=${COMPUTEHOP_DEVICE_NAME:-}
if [ -z "$device_name" ]; then
    device_name=$(hostname 2>/dev/null || printf '%s' "ComputeHop Worker")
fi
case "$device_name" in
    *'
'*) echo "COMPUTEHOP_DEVICE_NAME must not contain newlines." >&2; exit 1 ;;
esac
if [ "$#" -eq 0 ]; then
    set -- --lan-only
fi

for packaged_file in "$script_dir/bin/computehop" "$script_dir/bin/computehopd" "$script_dir/run-worker.sh"; do
    if [ ! -x "$packaged_file" ]; then
        echo "Packaged worker file is missing or not executable: $packaged_file" >&2
        exit 1
    fi
done

data_home=${XDG_DATA_HOME:-"$HOME/.local/share"}
config_home=${XDG_CONFIG_HOME:-"$HOME/.config"}
install_dir="$data_home/computehop/worker"
service_dir="$config_home/systemd/user"
service_file="$service_dir/computehop-worker.service"
installed_runner="$install_dir/run-installed-worker.sh"

escaped_device_name=$(printf '%s' "$device_name" | sed 's/\\/\\\\/g; s/"/\\"/g')
escaped_installed_runner=$(printf '%s' "$installed_runner" | sed 's/\\/\\\\/g; s/"/\\"/g')
shell_quote() {
    case "$1" in
        *'
'*) echo "Daemon arguments must not contain newlines." >&2; exit 1 ;;
    esac
    printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\"'\"'/g")"
}

if [ "$check_only" = true ]; then
    echo "Worker install check passed."
    echo "Would install worker files to: $install_dir"
    echo "Would install systemd user service: $service_file"
    echo "Would run daemon as worker: $device_name"
    printf 'Would pass daemon arguments:'
    for argument in "$@"; do
        printf ' %s' "$(shell_quote "$argument")"
    done
    printf '\n'
    exit 0
fi

mkdir -p "$install_dir/bin" "$service_dir"
install -m 755 "$script_dir/bin/computehop" "$install_dir/bin/computehop"
install -m 755 "$script_dir/bin/computehopd" "$install_dir/bin/computehopd"
install -m 755 "$script_dir/run-worker.sh" "$install_dir/run-worker.sh"

{
    printf '#!/bin/sh\n'
    printf 'set -eu\n'
    printf 'exec "%s/run-worker.sh"' "$install_dir"
    for argument in "$@"; do
        printf ' %s' "$(shell_quote "$argument")"
    done
    printf '\n'
} >"$installed_runner"
chmod 755 "$installed_runner"
cat >"$service_file" <<EOF
[Unit]
Description=ComputeHop worker
After=network-online.target

[Service]
Type=simple
Environment="COMPUTEHOP_DEVICE_NAME=$escaped_device_name"
ExecStart="$escaped_installed_runner"
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now computehop-worker.service

echo "Installed ComputeHop worker user service."
echo "Check status with: systemctl --user status computehop-worker.service"
echo "Confirm pairing requests with: $install_dir/bin/computehop connect confirm"
