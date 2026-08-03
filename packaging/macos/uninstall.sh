#!/bin/sh
set -eu

check_only=false

usage() {
    echo "Usage: packaging/macos/uninstall.sh [--check]" >&2
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
            usage
            exit 1
            ;;
    esac
done

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
    if [ "$check_only" = true ]; then
        echo "Would unload launch agent: $service_label"
    else
        launchctl bootout "$launch_domain/$service_label"
    fi
fi

if [ -L "$cli_target" ]; then
    if [ "$(readlink "$cli_target")" = "$expected_cli_target" ]; then
        if [ "$check_only" = true ]; then
            echo "Would remove CLI link: $cli_target"
        else
            rm -- "$cli_target"
        fi
    else
        echo "Left unrelated CLI link untouched at $cli_target" >&2
    fi
elif [ -e "$cli_target" ]; then
    echo "Left unrelated item untouched at $cli_target" >&2
fi
if [ -e "$app_target" ] || [ -L "$app_target" ]; then
    existing_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' \
        "$app_target/Contents/Info.plist" 2>/dev/null || true)
    if [ "$existing_id" = "com.computehop.app" ]; then
        if [ "$check_only" = true ]; then
            echo "Would remove app: $app_target"
        else
            rm -rf -- "$app_target"
        fi
    else
        echo "Left unrelated item untouched at $app_target" >&2
    fi
fi
if [ -f "$launch_agent_target" ]; then
    configured_label=$(/usr/libexec/PlistBuddy -c 'Print :Label' "$launch_agent_target" 2>/dev/null || true)
    if [ "$configured_label" = "$service_label" ]; then
        if [ "$check_only" = true ]; then
            echo "Would remove launch agent: $launch_agent_target"
        else
            rm -- "$launch_agent_target"
        fi
    fi
fi

if [ "$check_only" = true ]; then
    echo "Uninstall check passed."
    echo "Would preserve durable jobs and pairings in $HOME/Library/Application Support/ComputeHop"
else
    echo "Uninstalled the ComputeHop app, CLI link, and launch agent."
    echo "Durable jobs and pairings remain in $HOME/Library/Application Support/ComputeHop"
fi
