#!/bin/sh
set -eu

open_app=true
device_role=orchestrator
device_name=""
connectivity_url=""
stun_server=""
turn_server=""
turn_username=""
turn_password=""
cache_size=""
lan_only=false
usage() {
    echo "Usage: packaging/macos/install.sh [--no-open] [--role orchestrator|worker] [--lan-only]" >&2
    echo "       [--device-name NAME] [--cache-size SIZE]" >&2
    echo "       [--connectivity-url HTTPS_URL --stun-server STUN_URI]" >&2
    echo "       [--turn-server TURN_URI --turn-username USER --turn-password PASSWORD]" >&2
}
while [ "$#" -gt 0 ]; do
    case "$1" in
        --no-open)
            open_app=false
            shift
            ;;
        --role)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            device_role=$2
            shift 2
            ;;
        --device-name)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            device_name=$2
            shift 2
            ;;
        --connectivity-url)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            connectivity_url=$2
            shift 2
            ;;
        --stun-server)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            stun_server=$2
            shift 2
            ;;
        --turn-server)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            turn_server=$2
            shift 2
            ;;
        --turn-username)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            turn_username=$2
            shift 2
            ;;
        --turn-password)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            turn_password=$2
            shift 2
            ;;
        --cache-size)
            [ "$#" -ge 2 ] || { usage; exit 1; }
            cache_size=$2
            [ -n "$cache_size" ] || { echo "--cache-size must not be empty." >&2; exit 1; }
            shift 2
            ;;
        --lan-only)
            lan_only=true
            shift
            ;;
        *)
            usage
            exit 1
            ;;
    esac
done
case "$device_role" in
    orchestrator|worker) ;;
    *) echo "--role must be orchestrator or worker." >&2; exit 1 ;;
esac
if [ "$lan_only" = true ] && { [ -n "$connectivity_url" ] || [ -n "$stun_server" ] || \
    [ -n "$turn_server" ] || [ -n "$turn_username" ] || [ -n "$turn_password" ]; }; then
    echo "--lan-only cannot be combined with remote connectivity flags." >&2
    exit 1
fi
if { [ -n "$connectivity_url" ] && [ -z "$stun_server" ] && [ -z "$turn_server" ]; } || \
    { [ -z "$connectivity_url" ] && { [ -n "$stun_server" ] || [ -n "$turn_server" ]; }; }; then
    echo "--connectivity-url and at least one --stun-server or --turn-server must be supplied together." >&2
    exit 1
fi
if [ -n "$turn_server" ] && { [ -z "$turn_username" ] || [ -z "$turn_password" ]; }; then
    echo "--turn-server requires --turn-username and --turn-password." >&2
    exit 1
fi
if [ -z "$turn_server" ] && { [ -n "$turn_username" ] || [ -n "$turn_password" ]; }; then
    echo "--turn-username and --turn-password require --turn-server." >&2
    exit 1
fi
if [ -n "$connectivity_url" ]; then
    case "$connectivity_url" in
        https://?*) ;;
        *) echo "--connectivity-url must be an HTTPS URL." >&2; exit 1 ;;
    esac
    if [ -n "$stun_server" ]; then
        case "$stun_server" in
            stun:*|stuns:*) ;;
            *) echo "--stun-server must begin with stun: or stuns:." >&2; exit 1 ;;
        esac
    fi
    if [ -n "$turn_server" ]; then
        case "$turn_server" in
            turn:*|turns:*) ;;
            *) echo "--turn-server must begin with turn: or turns:." >&2; exit 1 ;;
        esac
    fi
fi
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
else
    status_output=$("$built_cli" status 2>&1) && {
        echo "A manually started computehopd is already running." >&2
        echo "Stop it with Ctrl-C, then run this installer again." >&2
        exit 1
    }
    case "$status_output" in
        *"ComputeHop daemon does not match this CLI"*)
            echo "A manually started computehopd from another build is already running." >&2
            echo "Stop its terminal with Ctrl-C, or run make uninstall-macos if it came from the development app." >&2
            echo "Then run this installer again." >&2
            exit 1
            ;;
    esac
fi

expected_cli_target="$app_target/Contents/Resources/bin/computehop"
expected_daemon_target="$app_target/Contents/Resources/bin/computehopd"
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
/usr/bin/plutil -remove ProgramArguments.0 "$launch_agent_target"
/usr/bin/plutil -insert ProgramArguments.0 -string \
    "$expected_daemon_target" "$launch_agent_target"
/usr/bin/plutil -remove ProgramArguments.2 "$launch_agent_target"
/usr/bin/plutil -insert ProgramArguments.2 -string "$device_role" "$launch_agent_target"
if [ -n "$device_name" ]; then
    /usr/bin/plutil -insert ProgramArguments -string "--device-name" -append "$launch_agent_target"
    /usr/bin/plutil -insert ProgramArguments -string "$device_name" -append "$launch_agent_target"
fi
if [ -n "$connectivity_url" ]; then
    /usr/bin/plutil -insert ProgramArguments -string "--connectivity-url" -append "$launch_agent_target"
    /usr/bin/plutil -insert ProgramArguments -string "$connectivity_url" -append "$launch_agent_target"
    if [ -n "$stun_server" ]; then
        /usr/bin/plutil -insert ProgramArguments -string "--stun-server" -append "$launch_agent_target"
        /usr/bin/plutil -insert ProgramArguments -string "$stun_server" -append "$launch_agent_target"
    fi
    if [ -n "$turn_server" ]; then
        /usr/bin/plutil -insert ProgramArguments -string "--turn-server" -append "$launch_agent_target"
        /usr/bin/plutil -insert ProgramArguments -string "$turn_server" -append "$launch_agent_target"
        /usr/bin/plutil -insert ProgramArguments -string "--turn-username" -append "$launch_agent_target"
        /usr/bin/plutil -insert ProgramArguments -string "$turn_username" -append "$launch_agent_target"
        /usr/bin/plutil -insert ProgramArguments -string "--turn-password" -append "$launch_agent_target"
        /usr/bin/plutil -insert ProgramArguments -string "$turn_password" -append "$launch_agent_target"
    fi
fi
if [ "$lan_only" = true ]; then
    /usr/bin/plutil -insert ProgramArguments -string "--lan-only" -append "$launch_agent_target"
fi
if [ -n "$cache_size" ]; then
    /usr/bin/plutil -insert ProgramArguments -string "--cache-size" -append "$launch_agent_target"
    /usr/bin/plutil -insert ProgramArguments -string "$cache_size" -append "$launch_agent_target"
fi
/usr/libexec/PlistBuddy -c "Set :StandardErrorPath $log_dir/daemon.log" "$launch_agent_target"
/usr/libexec/PlistBuddy -c "Set :StandardOutPath $log_dir/daemon.log" "$launch_agent_target"
/usr/libexec/PlistBuddy -c "Set :WorkingDirectory $HOME" "$launch_agent_target"
chmod 0644 "$launch_agent_target"
plutil -lint "$launch_agent_target" >/dev/null
check_plist_value "$launch_agent_target" Label "$service_label"
check_plist_value "$launch_agent_target" ProgramArguments:0 "$expected_daemon_target"
check_plist_value "$launch_agent_target" ProgramArguments:1 "--role"
check_plist_value "$launch_agent_target" ProgramArguments:2 "$device_role"
check_plist_value "$launch_agent_target" StandardErrorPath "$log_dir/daemon.log"
check_plist_value "$launch_agent_target" StandardOutPath "$log_dir/daemon.log"
check_plist_value "$launch_agent_target" WorkingDirectory "$HOME"

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
echo "The $device_role daemon now starts automatically when you log in."
if [ -n "$connectivity_url" ]; then
    echo "Direct remote connectivity is enabled through $connectivity_url"
    if [ -n "$turn_server" ]; then
        echo "TURN relay fallback is configured through $turn_server"
    fi
elif [ "$lan_only" = true ]; then
    echo "Remote connectivity is disabled; this install uses same-LAN discovery only."
fi
echo "CLI: $cli_target"
cli_command="computehop"
case ":$PATH:" in
    *:"$cli_dir":*) ;;
    *)
        cli_command="$cli_target"
        echo "Add $cli_dir to PATH to run 'computehop' without its full path."
        ;;
esac
echo ""
echo "Next:"
if [ "$device_role" = "orchestrator" ]; then
    echo "- Check readiness: $cli_command doctor"
    echo "- Install or start a worker on another computer on the same LAN."
    echo "- Connect one nearby worker: $cli_command connect nearby"
    echo "- Smoke test: $cli_command run --on auto hostname"
else
    echo "- On the orchestrator Mac, run: computehop connect nearby"
    echo "- On this worker, confirm the matching code in the menu bar or run: $cli_command connect confirm"
fi
