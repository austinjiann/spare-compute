#!/bin/sh
set -eu

if [ "$(uname -s)" != "Darwin" ]; then
    echo "The macOS uninstaller must run on macOS." >&2
    exit 1
fi

user_id=$(id -u)
launch_domain="gui/$user_id"
service_label="com.computehop.daemon"
app_target="$HOME/Applications/ComputeHop.app"
cli_target="$HOME/.local/bin/computehop"
expected_cli_target="$app_target/Contents/Resources/bin/computehop"
launch_agent_target="$HOME/Library/LaunchAgents/$service_label.plist"

if launchctl print "$launch_domain/$service_label" >/dev/null 2>&1; then
    launchctl bootout "$launch_domain/$service_label"
fi

if [ -L "$cli_target" ] && [ "$(readlink "$cli_target")" = "$expected_cli_target" ]; then
    rm -- "$cli_target"
fi
if [ -e "$app_target" ] || [ -L "$app_target" ]; then
    existing_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' \
        "$app_target/Contents/Info.plist" 2>/dev/null || true)
    if [ "$existing_id" = "com.computehop.app" ]; then
        rm -rf -- "$app_target"
    else
        echo "Left unrelated item untouched at $app_target" >&2
    fi
fi
if [ -f "$launch_agent_target" ]; then
    configured_label=$(/usr/libexec/PlistBuddy -c 'Print :Label' "$launch_agent_target" 2>/dev/null || true)
    if [ "$configured_label" = "$service_label" ]; then
        rm -- "$launch_agent_target"
    fi
fi

echo "Uninstalled the ComputeHop app, CLI link, and launch agent."
echo "Durable jobs and pairings remain in $HOME/Library/Application Support/ComputeHop"
