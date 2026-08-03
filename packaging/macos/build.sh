#!/bin/sh
set -eu

if [ "$(uname -s)" != "Darwin" ]; then
    echo "macOS packaging must run on macOS." >&2
    exit 1
fi

for tool in go node npm swift plutil codesign; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        echo "Required tool is missing: $tool" >&2
        exit 1
    fi
done

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repository_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd -P)
output_dir=${1:-"$repository_dir/dist/macos"}
case "$output_dir" in
    /*) ;;
    *) output_dir="$repository_dir/$output_dir" ;;
esac

version=${COMPUTEHOP_VERSION:-$(tr -d '\r\n' < "$repository_dir/VERSION")}
build_number=${COMPUTEHOP_BUILD_NUMBER:-1}
codesign_identity=${COMPUTEHOP_CODESIGN_IDENTITY:-"-"}
codesign_entitlements=${COMPUTEHOP_CODESIGN_ENTITLEMENTS:-"$script_dir/entitlements.plist"}
if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+(\.[0-9]+){1,2}$'; then
    echo "COMPUTEHOP_VERSION must look like 1.2 or 1.2.3." >&2
    exit 1
fi
if ! printf '%s\n' "$build_number" | grep -Eq '^[1-9][0-9]*$'; then
    echo "COMPUTEHOP_BUILD_NUMBER must be a positive integer." >&2
    exit 1
fi

app_bundle="$output_dir/ComputeHop.app"
if [ -e "$app_bundle" ] || [ -L "$app_bundle" ]; then
    rm -rf -- "$app_bundle"
fi
mkdir -p "$app_bundle/Contents/MacOS" "$app_bundle/Contents/Resources/bin"

swift build --package-path "$repository_dir" -c release --product ComputeHop
swift_bin_dir=$(swift build --package-path "$repository_dir" -c release --show-bin-path)
cp "$swift_bin_dir/ComputeHop" "$app_bundle/Contents/MacOS/ComputeHop"

control_center_dir="$repository_dir/apps/control-center"
node_arch=$(node -p 'process.arch')
control_center_package_dir="$control_center_dir/.out/ComputeHop Control Center-darwin-$node_arch"
control_center_app="$control_center_package_dir/ComputeHop Control Center.app"
npm --prefix "$control_center_dir" run package:dir
if [ ! -d "$control_center_app" ]; then
    echo "Control Center package was not created at $control_center_app" >&2
    exit 1
fi
cp -R "$control_center_app" "$app_bundle/Contents/Resources/ComputeHop Control Center.app"

(
    cd "$repository_dir"
    go build -trimpath -ldflags "-s -w -X main.version=$version" \
        -o "$app_bundle/Contents/Resources/bin/computehop" ./cmd/computehop
    go build -trimpath -ldflags "-s -w -X main.version=$version" \
        -o "$app_bundle/Contents/Resources/bin/computehopd" ./cmd/computehopd
)

cp "$script_dir/Info.plist" "$app_bundle/Contents/Info.plist"
plutil -replace CFBundleShortVersionString -string "$version" "$app_bundle/Contents/Info.plist"
plutil -replace CFBundleVersion -string "$build_number" "$app_bundle/Contents/Info.plist"

if [ "$codesign_identity" = "-" ]; then
    codesign --force --deep --sign - "$app_bundle"
else
    if [ -n "$codesign_entitlements" ]; then
        codesign --force --deep --options runtime --timestamp \
            --entitlements "$codesign_entitlements" \
            --sign "$codesign_identity" "$app_bundle"
    else
        codesign --force --deep --options runtime --timestamp \
            --sign "$codesign_identity" "$app_bundle"
    fi
fi
"$script_dir/verify.sh" "$app_bundle"
echo "Built $app_bundle"
