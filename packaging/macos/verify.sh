#!/bin/sh
set -eu

if [ "$(uname -s)" != "Darwin" ]; then
    echo "macOS bundle verification must run on macOS." >&2
    exit 1
fi

app_bundle=${1:-}
if [ -z "$app_bundle" ] || [ ! -d "$app_bundle" ]; then
    echo "Usage: packaging/macos/verify.sh /path/to/ComputeHop.app" >&2
    exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
launch_agent_template="$script_dir/com.computehop.daemon.plist"
info_plist="$app_bundle/Contents/Info.plist"
app_executable="$app_bundle/Contents/MacOS/ComputeHop"
cli_executable="$app_bundle/Contents/Resources/bin/computehop"
daemon_executable="$app_bundle/Contents/Resources/bin/computehopd"
control_center_app="$app_bundle/Contents/Resources/ComputeHop Control Center.app"
control_center_info_plist="$control_center_app/Contents/Info.plist"
control_center_executable="$control_center_app/Contents/MacOS/ComputeHop"
control_center_daemon="$control_center_app/Contents/Resources/bin/computehopd"

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

plutil -lint "$info_plist" >/dev/null
check_plist_value "$info_plist" CFBundleIdentifier "com.computehop.app"
for executable in "$app_executable" "$cli_executable" "$daemon_executable"; do
    if [ ! -x "$executable" ]; then
        echo "Bundle executable is missing: $executable" >&2
        exit 1
    fi
done
if [ ! -d "$control_center_app" ]; then
    echo "Embedded Control Center app is missing: $control_center_app" >&2
    exit 1
fi
plutil -lint "$control_center_info_plist" >/dev/null
check_plist_value "$control_center_info_plist" CFBundleIdentifier "com.computehop.controlcenter"
for executable in "$control_center_executable" "$control_center_daemon"; do
    if [ ! -x "$executable" ]; then
        echo "Embedded Control Center executable is missing: $executable" >&2
        exit 1
    fi
done

plutil -lint "$launch_agent_template" >/dev/null
check_plist_value "$launch_agent_template" Label "com.computehop.daemon"
check_plist_value "$launch_agent_template" ProgramArguments:0 "DAEMON_PATH"
check_plist_value "$launch_agent_template" ProgramArguments:1 "--role"
check_plist_value "$launch_agent_template" ProgramArguments:2 "orchestrator"
check_plist_value "$launch_agent_template" KeepAlive "true"
check_plist_value "$launch_agent_template" ProcessType "Background"
check_plist_value "$launch_agent_template" RunAtLoad "true"
check_plist_value "$launch_agent_template" StandardErrorPath "STDERR_PATH"
check_plist_value "$launch_agent_template" StandardOutPath "STDOUT_PATH"
check_plist_value "$launch_agent_template" WorkingDirectory "WORKING_DIRECTORY"

codesign --verify --deep --strict "$app_bundle"
signature=$(/usr/bin/codesign -dv --verbose=4 "$app_bundle" 2>&1 || true)
if [ "${COMPUTEHOP_VERIFY_DEVELOPER_ID:-false}" = true ]; then
    case "$signature" in
        *"Authority=Developer ID Application"*) ;;
        *)
            echo "App is not signed with a Developer ID Application certificate." >&2
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
fi
if [ "${COMPUTEHOP_VERIFY_NOTARIZED:-false}" = true ]; then
    if ! command -v xcrun >/dev/null 2>&1; then
        echo "Required tool is missing for notarization verification: xcrun" >&2
        exit 1
    fi
    xcrun stapler validate "$app_bundle" >/dev/null
fi
"$cli_executable" version >/dev/null
"$daemon_executable" --version >/dev/null
"$control_center_daemon" --version >/dev/null
if [ -f "$script_dir/verify-control-center-background.js" ]; then
    if command -v node >/dev/null 2>&1; then
        node "$script_dir/verify-control-center-background.js" "$app_bundle" >/dev/null
    else
        echo "Skipped Control Center background resolver check because node is unavailable." >&2
    fi
fi
echo "Verified $app_bundle and launch agent template"
