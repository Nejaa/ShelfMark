#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
require_command() {
    if ! command -v "$1" >/dev/null; then
        echo "Missing build dependency: $1 (target: $target_os/$target_arch)." >&2
        echo 'See README.md > Building for platform prerequisites. HTTP-only builds use --headless.' >&2
        exit 1
    fi
}
usage() { echo 'Usage: ./build-target.sh <linux|windows|darwin> [amd64|arm64|386] [--headless]' >&2; }
if [[ $# -lt 1 ]]; then usage; exit 2; fi
target_os=$1
shift
target_arch=amd64
if [[ ${1:-} != --headless && $# -gt 0 ]]; then target_arch=$1; shift; fi
headless=false
if [[ ${1:-} == --headless ]]; then headless=true; shift; fi
if [[ $# -ne 0 ]]; then usage; exit 2; fi
case "$target_os" in linux|windows|darwin) ;; *) usage; exit 2 ;; esac
case "$target_arch" in amd64|arm64|386) ;; *) usage; exit 2 ;; esac
require_command go

mkdir -p target
output="target/shelfmark-${target_os}-${target_arch}"
if [[ "$target_os" == windows ]]; then output+=.exe; fi
# Windowed PE binaries do not allocate a console on Explorer launches. The main
# process can attach to a terminal that already exists; the helper uses pipes.
link_flags='-s -w'
if [[ "$target_os" == windows ]]; then link_flags+=' -H=windowsgui'; fi
require_command python3
require_command cmake
require_command make

host_os=$(go env GOHOSTOS)
host_arch=$(go env GOHOSTARCH)
if [[ "$target_os" != windows && ( "$target_os" != "$host_os" || "$target_arch" != "$host_arch" ) ]]; then
    echo "$target_os native runtime packaging currently requires a matching host OS and architecture." >&2
    exit 1
fi
if [[ "$target_os" == windows && "$host_os" != windows ]]; then
    case "$target_arch" in
        amd64) compiler_prefix=x86_64-w64-mingw32 ;;
        386) compiler_prefix=i686-w64-mingw32 ;;
        arm64) compiler_prefix=aarch64-w64-mingw32 ;;
    esac
    export CC="${WINDOWS_CC:-${CC:-$compiler_prefix-gcc}}"
    export CXX="${WINDOWS_CXX:-${CXX:-$compiler_prefix-g++}}"
    for compiler in "$CC" "$CXX"; do
        if ! command -v "$compiler" >/dev/null; then
            echo "Missing Windows cross-compiler: $compiler. Install MinGW-w64 or set WINDOWS_CC and WINDOWS_CXX." >&2
            exit 1
        fi
    done
fi

build_dir=$(mktemp -d "$PWD/target/.build-${target_os}-${target_arch}-XXXXXX")
trap 'rm -rf "$build_dir"' EXIT
ocr_payload="$build_dir/ocr.tar.gz"
python3 runtime/ocr/package.py --target-os "$target_os" --target-arch "$target_arch" \
    --cache "$PWD/target/cache/ocr" --output "$ocr_payload"
python3 - "$PWD/internal/ocrruntime/payload.tar.gz" "$ocr_payload" "$build_dir/overlay.json" <<'PYTHON'
import json, sys
from pathlib import Path
Path(sys.argv[3]).write_text(json.dumps({"Replace": {sys.argv[1]: sys.argv[2]}}))
PYTHON
if "$headless"; then
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -overlay "$build_dir/overlay.json" -tags ocr -trimpath -ldflags="$link_flags" -o "$output" ./cmd/shelfmark
    echo "Built $output (HTTP only, bundled OCR)"
    exit 0
fi
helper="$build_dir/shelfmark-window"
if [[ "$target_os" == windows ]]; then helper+=.exe; fi
payload="$build_dir/payload.tar.gz"
package_args=(--target-os "$target_os" --target-arch "$target_arch" --helper "$helper" --output "$payload")
if [[ "$target_os" == linux && -n "${WEBVIEW_SYSROOT:-}" ]]; then
    require_command gcc
    sysroot=$(cd "$WEBVIEW_SYSROOT" && pwd)
    triplet=$(gcc -dumpmachine)
    export PATH="$sysroot/usr/bin:$PATH"
    export PKG_CONFIG_SYSROOT_DIR="$sysroot"
    export PKG_CONFIG_LIBDIR="$sysroot/usr/lib/$triplet/pkgconfig:$sysroot/usr/share/pkgconfig"
    export CGO_CFLAGS="-I$sysroot/usr/include ${CGO_CFLAGS:-}"
    export CGO_CXXFLAGS="-I$sysroot/usr/include ${CGO_CXXFLAGS:-}"
    export CGO_LDFLAGS="-L$sysroot/usr/lib/$triplet -Wl,-rpath-link,$sysroot/usr/lib/$triplet ${CGO_LDFLAGS:-}"
    export LD_LIBRARY_PATH="$sysroot/usr/lib/$triplet:$sysroot/lib/$triplet${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
    package_args+=(--sysroot "$sysroot")
fi
if [[ "$target_os" == linux ]]; then
    require_command "${CC:-gcc}"
    require_command "${CXX:-g++}"
    require_command gcc
    require_command ldd
    require_command dpkg-query
    require_command "${PKG_CONFIG:-pkg-config}"
    if ! "${PKG_CONFIG:-pkg-config}" --exists gtk+-3.0 webkit2gtk-4.0; then
        echo 'Missing GTK 3 or WebKitGTK 4.0 development metadata.' >&2
        "${PKG_CONFIG:-pkg-config}" --print-errors --exists gtk+-3.0 webkit2gtk-4.0 >&2 || true
        echo 'Install the Linux desktop build dependencies listed in README.md, or set WEBVIEW_SYSROOT to a complete dependency root.' >&2
        exit 1
    fi
fi
if [[ "$target_os" == windows ]]; then
    # The pinned WebView2 SDK includes EventToken.h using Windows casing.
    export CGO_CPPFLAGS="-I$PWD/desktop/webview/windows/include ${CGO_CPPFLAGS:-}"
    # The binding's built-in loader ignores the fixed-runtime environment
    # override. Use the bundled Microsoft loader without a system fallback.
    export CGO_CXXFLAGS="-DWEBVIEW_MSWEBVIEW2_BUILTIN_IMPL=0 -DWEBVIEW_MSWEBVIEW2_EXPLICIT_LINK=1 ${CGO_CXXFLAGS:-}"
    if [[ -z "${WEBVIEW2_RUNTIME_DIR:-}" ]]; then
        WEBVIEW2_RUNTIME_DIR=$(python3 -m desktop.webview.windows.runtime --arch "$target_arch" --cache "$PWD/target/cache/webview2")
    fi
    package_args+=(--webview2-runtime "$WEBVIEW2_RUNTIME_DIR")
    webview2_loader=$(python3 -m desktop.webview.windows.runtime --arch "$target_arch" --cache "$PWD/target/cache/webview2" --loader-only)
    package_args+=(--webview2-loader "$webview2_loader")
fi
CGO_ENABLED=1 GOOS="$target_os" GOARCH="$target_arch" go build -tags webview_helper -trimpath -ldflags="$link_flags" -o "$helper" ./cmd/shelfmark-window
python3 desktop/webview/package.py "${package_args[@]}"

# Give this build its own embedded archive, without replacing another target's
# payload or writing generated runtime files into the source tree.
python3 - "$PWD/internal/desktop/payload.tar.gz" "$payload" "$build_dir/overlay.json" <<'PYTHON'
import json, sys
from pathlib import Path
overlay = Path(sys.argv[3])
contents = json.loads(overlay.read_text())
contents["Replace"][sys.argv[1]] = sys.argv[2]
overlay.write_text(json.dumps(contents))
PYTHON
CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -overlay "$build_dir/overlay.json" -tags desktop,ocr -trimpath -ldflags="$link_flags" -o "$output" ./cmd/shelfmark

echo "Built $output (embedded desktop helper and OCR)"
