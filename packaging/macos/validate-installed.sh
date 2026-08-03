#!/bin/sh
set -eu

expected_role=""
expected_device_name=""
expect_lan_only=""
run_local_smoke=false

usage() {
    echo "Usage: packaging/macos/validate-installed.sh [--role orchestrator|worker]" >&2
    echo "       [--device-name NAME] [--lan-only|--remote-enabled] [--run-local-smoke]" >&2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --role)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            expected_role=$2
            shift 2
            ;;
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
        --run-local-smoke)
            run_local_smoke=true
            shift
            ;;
        *)
            usage
            exit 1
            ;;
    esac
done

case "$expected_role" in
    ""|orchestrator|worker) ;;
    *) echo "--role must be orchestrator or worker." >&2; exit 1 ;;
esac

if [ "$(uname -s)" != "Darwin" ]; then
    echo "Installed macOS validation must run on macOS." >&2
    exit 1
fi

for tool in launchctl plutil; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "Required tool is missing: $tool" >&2
        exit 1
    fi
done

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
user_id=$(id -u)
launch_domain="gui/$user_id"
service_label="com.computehop.daemon"
app_target="$HOME/Applications/ComputeHop.app"
cli_target="$HOME/.local/bin/computehop"
expected_cli_target="$app_target/Contents/Resources/bin/computehop"
expected_daemon_target="$app_target/Contents/Resources/bin/computehopd"
launch_agent_target="$HOME/Library/LaunchAgents/$service_label.plist"

check_plist_value() {
    plist_path=$1
    key_path=$2
    expected_value=$3
    actual_value=$(/usr/libexec/PlistBuddy -c "Print :$key_path" "$plist_path" 2>/dev/null || true)
    if [ "$actual_value" != "$expected_value" ]; then
        echo "Unexpected $key_path in $plist_path: $actual_value" >&2
        echo "Expected: $expected_value" >&2
        exit 1
    fi
}

plist_argument_value() {
    plist_path=$1
    flag=$2
    index=0
    while argument=$(/usr/libexec/PlistBuddy -c "Print :ProgramArguments:$index" "$plist_path" 2>/dev/null); do
        if [ "$argument" = "$flag" ]; then
            value_index=$((index + 1))
            /usr/libexec/PlistBuddy -c "Print :ProgramArguments:$value_index" "$plist_path" 2>/dev/null || true
            return
        fi
        index=$((index + 1))
    done
}

plist_argument_present() {
    plist_path=$1
    flag=$2
    index=0
    while argument=$(/usr/libexec/PlistBuddy -c "Print :ProgramArguments:$index" "$plist_path" 2>/dev/null); do
        if [ "$argument" = "$flag" ]; then
            return 0
        fi
        index=$((index + 1))
    done
    return 1
}

if [ ! -d "$app_target" ]; then
    echo "Installed app is missing: $app_target" >&2
    exit 1
fi
"$script_dir/verify.sh" "$app_target" >/dev/null

if [ ! -L "$cli_target" ]; then
    echo "CLI link is missing: $cli_target" >&2
    exit 1
fi
actual_cli_target=$(readlink "$cli_target" 2>/dev/null || true)
if [ "$actual_cli_target" != "$expected_cli_target" ]; then
    echo "Unexpected CLI link target: $actual_cli_target" >&2
    echo "Expected: $expected_cli_target" >&2
    exit 1
fi
if [ ! -x "$cli_target" ]; then
    echo "CLI is not executable: $cli_target" >&2
    exit 1
fi

if [ ! -f "$launch_agent_target" ]; then
    echo "LaunchAgent is missing: $launch_agent_target" >&2
    exit 1
fi
plutil -lint "$launch_agent_target" >/dev/null
check_plist_value "$launch_agent_target" Label "$service_label"
check_plist_value "$launch_agent_target" ProgramArguments:0 "$expected_daemon_target"
check_plist_value "$launch_agent_target" ProgramArguments:1 "--role"
if [ -n "$expected_role" ]; then
    check_plist_value "$launch_agent_target" ProgramArguments:2 "$expected_role"
fi
if [ -n "$expected_device_name" ]; then
    actual_device_name=$(plist_argument_value "$launch_agent_target" "--device-name")
    if [ "$actual_device_name" != "$expected_device_name" ]; then
        echo "Unexpected --device-name in $launch_agent_target: $actual_device_name" >&2
        echo "Expected: $expected_device_name" >&2
        exit 1
    fi
fi
if [ "$expect_lan_only" = true ] && ! plist_argument_present "$launch_agent_target" "--lan-only"; then
    echo "Expected LaunchAgent to include --lan-only." >&2
    exit 1
fi
if [ "$expect_lan_only" = false ] && plist_argument_present "$launch_agent_target" "--lan-only"; then
    echo "Expected LaunchAgent to allow remote connectivity, but --lan-only is present." >&2
    exit 1
fi

if ! launchctl print "$launch_domain/$service_label" >/dev/null 2>&1; then
    echo "ComputeHop LaunchAgent is installed but not loaded: $launch_domain/$service_label" >&2
    exit 1
fi

"$cli_target" version >/dev/null
status_output=$("$cli_target" status)
if [ -n "$expected_role" ] && ! printf '%s\n' "$status_output" | grep -q "($expected_role,"; then
    echo "Daemon status did not report expected role: $expected_role" >&2
    printf '%s\n' "$status_output" >&2
    exit 1
fi
"$cli_target" doctor >/dev/null

if [ "$run_local_smoke" = true ]; then
    if [ "$expected_role" = "worker" ]; then
        echo "--run-local-smoke is only valid for an orchestrator install." >&2
        exit 1
    fi
    "$cli_target" run --wait hostname >/dev/null
fi

echo "Installed ComputeHop validation passed."
echo "App: $app_target"
echo "CLI: $cli_target -> $expected_cli_target"
echo "LaunchAgent: $launch_agent_target"
