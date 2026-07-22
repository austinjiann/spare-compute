#!/bin/sh
set -eu

open_app=true
case "${1:-}" in
    '') ;;
    --no-open) open_app=false ;;
    *) echo "Usage: packaging/macos/install.sh [--no-open]" >&2; exit 1 ;;
esac

if [ "$(uname -s)" != "Darwin" ]; then
    echo "The macOS installer must run on macOS." >&2
    exit 1
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/computehop-install.XXXXXX")
service_was_loaded=false
install_complete=false
launch_domain=""
launch_agent_target=""
cleanup() {
    if [ "$service_was_loaded" = true ] && [ "$install_complete" = false ] && \
        [ -n "$launch_domain" ] && [ -f "$launch_agent_target" ]; then
        launchctl bootstrap "$launch_domain" "$launch_agent_target" >/dev/null 2>&1 || true
    fi
    rm -rf -- "$temporary_dir"
}
trap cleanup EXIT HUP INT TERM

"$script_dir/build.sh" "$temporary_dir"
built_app="$temporary_dir/ComputeHop.app"
built_cli="$built_app/Contents/Resources/bin/computehop"

user_id=$(id -u)
launch_domain="gui/$user_id"
service_label="com.computehop.daemon"
applications_dir="$HOME/Applications"
app_target="$applications_dir/ComputeHop.app"
cli_dir="$HOME/.local/bin"
cli_target="$cli_dir/computehop"
launch_agents_dir="$HOME/Library/LaunchAgents"
launch_agent_target="$launch_agents_dir/$service_label.plist"
log_dir="$HOME/Library/Logs/ComputeHop"

if [ -e "$app_target" ] || [ -L "$app_target" ]; then
    existing_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' \
        "$app_target/Contents/Info.plist" 2>/dev/null || true)
    if [ "$existing_id" != "com.computehop.app" ]; then
        echo "Refusing to replace an unrelated item at $app_target" >&2
        exit 1
    fi
fi
if [ -f "$launch_agent_target" ]; then
    existing_label=$(/usr/libexec/PlistBuddy -c 'Print :Label' "$launch_agent_target" 2>/dev/null || true)
    if [ "$existing_label" != "$service_label" ]; then
        echo "Refusing to replace an unrelated item at $launch_agent_target" >&2
        exit 1
    fi
fi

if launchctl print "$launch_domain/$service_label" >/dev/null 2>&1; then
    service_was_loaded=true
    launchctl bootout "$launch_domain/$service_label"
elif "$built_cli" status >/dev/null 2>&1; then
    echo "A manually started computehopd is already running." >&2
    echo "Stop it with Ctrl-C, then run this installer again." >&2
    exit 1
fi

expected_cli_target="$app_target/Contents/Resources/bin/computehop"
if [ -e "$cli_target" ] || [ -L "$cli_target" ]; then
    existing_cli_target=$(readlink "$cli_target" 2>/dev/null || true)
    if [ "$existing_cli_target" != "$expected_cli_target" ]; then
        echo "Refusing to replace an unrelated item at $cli_target" >&2
        exit 1
    fi
fi

mkdir -p "$applications_dir" "$cli_dir" "$launch_agents_dir" "$log_dir"
chmod 0700 "$log_dir"
if [ -e "$app_target" ] || [ -L "$app_target" ]; then
    rm -rf -- "$app_target"
fi
cp -R "$built_app" "$app_target"
ln -sfn "$expected_cli_target" "$cli_target"

cp "$script_dir/com.computehop.daemon.plist" "$launch_agent_target"
/usr/libexec/PlistBuddy -c "Set :ProgramArguments:0 $app_target/Contents/Resources/bin/computehopd" \
    "$launch_agent_target"
/usr/libexec/PlistBuddy -c "Set :StandardErrorPath $log_dir/daemon.log" "$launch_agent_target"
/usr/libexec/PlistBuddy -c "Set :StandardOutPath $log_dir/daemon.log" "$launch_agent_target"
/usr/libexec/PlistBuddy -c "Set :WorkingDirectory $HOME" "$launch_agent_target"
chmod 0644 "$launch_agent_target"
plutil -lint "$launch_agent_target" >/dev/null

launchctl bootstrap "$launch_domain" "$launch_agent_target"
launchctl kickstart -k "$launch_domain/$service_label"

attempt=0
until "$cli_target" status >/dev/null 2>&1; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 50 ]; then
        echo "ComputeHop was installed, but its daemon did not become ready." >&2
        echo "Inspect $log_dir/daemon.log" >&2
        exit 1
    fi
    sleep 0.1
done
install_complete=true

if [ "$open_app" = true ]; then
    open "$app_target"
fi

echo "Installed ComputeHop in $app_target"
echo "The daemon now starts automatically when you log in."
echo "CLI: $cli_target"
case ":$PATH:" in
    *:"$cli_dir":*) ;;
    *) echo "Add $cli_dir to PATH to run 'computehop' without its full path." ;;
esac
