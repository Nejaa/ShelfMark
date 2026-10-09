#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
if [[ $# -gt 1 || ( $# -eq 1 && "$1" != --headless ) ]]; then
    echo 'Usage: ./build.sh [--headless]' >&2
    exit 2
fi

./build-target.sh linux amd64 "$@"
./build-target.sh windows amd64 "$@"
