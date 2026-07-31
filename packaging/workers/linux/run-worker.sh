#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
device_name=${COMPUTEHOP_DEVICE_NAME:-}
if [ -z "$device_name" ]; then
    device_name=$(hostname 2>/dev/null || printf '%s' "ComputeHop Worker")
fi

exec "$script_dir/bin/computehopd" --role worker --device-name "$device_name" "$@"
