#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
workspace_dir=$(mktemp -d "${TMPDIR:-/tmp}/computehop-worker-smoke.XXXXXX")

cleanup() {
    rm -rf -- "$workspace_dir"
}
trap cleanup EXIT HUP INT TERM

package_dir="$workspace_dir/ComputeHop-worker-linux-amd64"
fake_bin="$workspace_dir/fake-bin"
data_home="$workspace_dir/data"
config_home="$workspace_dir/config"
mkdir -p "$package_dir/bin" "$fake_bin" "$data_home" "$config_home"

cp "$script_dir/linux/install-systemd-user.sh" "$package_dir/install-systemd-user.sh"
cp "$script_dir/linux/run-worker.sh" "$package_dir/run-worker.sh"
cp "$script_dir/linux/validate-installed-worker.sh" "$package_dir/validate-installed-worker.sh"
chmod 755 "$package_dir/install-systemd-user.sh" "$package_dir/run-worker.sh" "$package_dir/validate-installed-worker.sh"

for binary in computehop computehopd; do
{
    printf '#!/bin/sh\n'
    printf 'case "$1" in\n'
    printf '  version) exit 0 ;;\n'
    printf '  status) printf "computehopd dev ready\\nDevice: Smoke Worker (worker, abcdefgh)\\n"; exit 0 ;;\n'
    printf '  doctor) exit 0 ;;\n'
    printf '  *) exit 0 ;;\n'
    printf 'esac\n'
} >"$package_dir/bin/$binary"
    chmod 755 "$package_dir/bin/$binary"
done

{
    printf '#!/bin/sh\n'
    printf 'printf "Linux\\n"\n'
} >"$fake_bin/uname"
chmod 755 "$fake_bin/uname"

{
    printf '#!/bin/sh\n'
    printf 'case "$2" in\n'
    printf '  is-enabled|is-active) exit 0 ;;\n'
    printf '  *) exit 0 ;;\n'
    printf 'esac\n'
} >"$fake_bin/systemctl"
chmod 755 "$fake_bin/systemctl"

check_output=$(
    PATH="$fake_bin:$PATH" \
    HOME="$workspace_dir/home" \
    XDG_DATA_HOME="$data_home" \
    XDG_CONFIG_HOME="$config_home" \
    COMPUTEHOP_DEVICE_NAME="Smoke Worker" \
    "$package_dir/install-systemd-user.sh" --check --lan-only 2>&1
)

printf '%s\n' "$check_output" | grep -q "Worker install check passed."
printf '%s\n' "$check_output" | grep -q "Would run daemon as worker: Smoke Worker"
printf '%s\n' "$check_output" | grep -q "Would pass daemon arguments: '--lan-only'"

invalid_output=$(
    PATH="$fake_bin:$PATH" \
    HOME="$workspace_dir/home" \
    XDG_DATA_HOME="$data_home" \
    XDG_CONFIG_HOME="$config_home" \
    COMPUTEHOP_DEVICE_NAME="Smoke Worker" \
    "$package_dir/install-systemd-user.sh" \
        --check \
        --lan-only \
        --connectivity-url https://connect.example.com \
        --stun-server stun:turn.example.com:3478 2>&1 || true
)
printf '%s\n' "$invalid_output" | grep -q -- "--lan-only cannot be combined"

invalid_turn_output=$(
    PATH="$fake_bin:$PATH" \
    HOME="$workspace_dir/home" \
    XDG_DATA_HOME="$data_home" \
    XDG_CONFIG_HOME="$config_home" \
    COMPUTEHOP_DEVICE_NAME="Smoke Worker" \
    "$package_dir/install-systemd-user.sh" \
        --check \
        --connectivity-url https://connect.example.com \
        --turn-server turn:turn.example.com:3478 2>&1 || true
)
printf '%s\n' "$invalid_turn_output" | grep -q -- "--turn-server requires --turn-username and --turn-password"

invalid_repeated_stun_output=$(
    PATH="$fake_bin:$PATH" \
    HOME="$workspace_dir/home" \
    XDG_DATA_HOME="$data_home" \
    XDG_CONFIG_HOME="$config_home" \
    COMPUTEHOP_DEVICE_NAME="Smoke Worker" \
    "$package_dir/install-systemd-user.sh" \
        --check \
        --connectivity-url https://connect.example.com \
        --stun-server http://bad.example.com:3478 \
        --stun-server stun:turn.example.com:3478 2>&1 || true
)
printf '%s\n' "$invalid_repeated_stun_output" | grep -q -- "--stun-server must begin with stun: or stuns:"

if [ -e "$data_home/computehop" ]; then
    echo "Linux worker --check wrote worker files." >&2
    exit 1
fi
if [ -e "$config_home/systemd" ]; then
    echo "Linux worker --check wrote systemd files." >&2
    exit 1
fi

install_dir="$data_home/computehop/worker"
service_dir="$config_home/systemd/user"
service_file="$service_dir/computehop-worker.service"
mkdir -p "$install_dir/bin" "$service_dir"
cp "$package_dir/bin/computehop" "$install_dir/bin/computehop"
cp "$package_dir/bin/computehopd" "$install_dir/bin/computehopd"
cp "$package_dir/run-worker.sh" "$install_dir/run-worker.sh"
{
    printf '#!/bin/sh\n'
    printf 'exec "%s/run-worker.sh" --lan-only\n' "$install_dir"
} >"$install_dir/run-installed-worker.sh"
chmod 755 "$install_dir/bin/computehop" "$install_dir/bin/computehopd" \
    "$install_dir/run-worker.sh" "$install_dir/run-installed-worker.sh"
cat >"$service_file" <<EOF
[Unit]
Description=ComputeHop worker

[Service]
Environment="COMPUTEHOP_DEVICE_NAME=Smoke Worker"
ExecStart="$install_dir/run-installed-worker.sh"
EOF

validate_output=$(
    PATH="$fake_bin:$PATH" \
    HOME="$workspace_dir/home" \
    XDG_DATA_HOME="$data_home" \
    XDG_CONFIG_HOME="$config_home" \
    "$package_dir/validate-installed-worker.sh" --device-name "Smoke Worker" --lan-only 2>&1
)
printf '%s\n' "$validate_output" | grep -q "Installed ComputeHop worker validation passed."

echo "Worker package smoke passed."
