# Building Shelfmark

Run the commands below from the repository root. Container builds produce the
Linux and Windows release files without installing native development packages on
the host. Native builds let you build one target or customize its runtime bundle.

For running downloaded binaries or the container, see the [user guide](README.md).
For CI and publishing releases, see [MAINTAINER.md](MAINTAINER.md).

## Container builds

Install Docker with Buildx, Bash, Python 3, Git, and gzip. The Docker daemon must
be running. Go, C/C++ compilers and native development packages are installed
inside the build image; they are not required on the host.

```sh
./build-container.sh                 # Local dev build
./build-container.sh v0.1.0           # Versioned image and downloads
```

Output is under `target/releases/<version>/`: Linux and Windows amd64 executables,
license archives, the Docker image archive, project source, dependency sources,
and `SHA256SUMS`. Existing output directories are refused to avoid mixing builds.
The version argument labels the output and container image; it does not check out
a Git tag. Local builds package the current working tree, including local edits.

The first build downloads and compiles native dependencies and collects exact
source packages; allow several gigabytes of storage. Docker layers cache later
builds. Source collection needs network access and fails if the exact installed
package sources are unavailable.

The build host container is Linux/amd64; Windows uses MinGW cross-compilation.
Container-built Linux desktop and OCR helpers require **glibc 2.36 or newer**,
based on Debian 12. Plain HTTP service can still start if native helpers cannot
load, but OCR and the desktop window require that compatibility.

For just the HTTP image without release downloads:

```sh
docker buildx build --platform linux/amd64 --target runtime \
  --build-arg TARGET_OS=linux --build-arg BUILD_MODE=headless \
  --tag shelfmark:dev --load .
```

## HTTP-only Go build

For an HTTP-only executable without bundled OCR or desktop dependencies, install
the toolchain specified in [go.mod](go.mod):

```sh
CGO_ENABLED=0 go build -o shelfmark ./cmd/shelfmark
./shelfmark -headless
```

This build requires no C/C++ compiler, Python, or frontend tools. OCR requires an
optional Tesseract installation on the server's `PATH` with English or French
language data. To bundle OCR while omitting the desktop runtime, use
`./build-target.sh linux amd64 --headless` with the native prerequisites below.

## Common requirements

- The Go version specified in [go.mod](go.mod), or a compatible newer toolchain.
- Bash and standard shell utilities for the build scripts. On Windows, use a
  Bash environment such as MSYS2 or Git Bash.
- Python 3, available as `python3`, for native runtime packaging.
- CMake and Make for building the bundled OCR engine.
- A C/C++ toolchain targeting the requested OS and architecture for OCR and desktop
  helpers. The HTTP server itself is built with `CGO_ENABLED=0`.
- Access to download Go modules, OCR sources and models, and the Windows browser
  runtime on the first build, or populated dependency caches.

Scripted HTTP-only builds (`--headless`) include OCR and require the common native
build tools, but no GUI development packages. A direct `CGO_ENABLED=0 go build`
requires only Go and leaves OCR dependent on an optional host installation.

The OCR packager builds Tesseract 5.5.1 and Leptonica 1.85.0 as static components,
with compiler runtimes linked into the helper on Linux and Windows. Images are
decoded in Go, so native image-codec libraries are unnecessary. English and French
models come from the official `tessdata_best` collection. Versions, source URLs,
and SHA-256 checksums are recorded in `runtime/ocr/sources.json`; compiler-runtime
license files are pinned in `runtime/ocr/licenses.json`. Builds cache downloads
and native compilation under `target/cache/ocr/`. Keep the cache for offline
rebuilds. `OCR_BUILD_JOBS` controls compilation parallelism (default: 4), and
`OCR_CC` / `OCR_CXX` can override the OCR compilers. MinGW builds prefer the POSIX
threading variant when available.

## Linux desktop

Build on Linux with the same CPU architecture as the target. The packager currently
supports Debian-style multiarch layouts and WebKitGTK's **4.0 ABI**. The pinned
binding does not use the 4.1 ABI; distributions providing only 4.1 need a compatible
4.0 dependency root through `WEBVIEW_SYSROOT`.

On a Debian-based build machine providing the 4.0 packages:

```sh
sudo apt-get install build-essential cmake pkg-config python3 libgtk-3-dev \
  libwebkit2gtk-4.0-dev libglib2.0-bin glib-networking \
  libgdk-pixbuf2.0-bin libegl-mesa0 libglx-mesa0 libgl1-mesa-dri \
  bubblewrap xdg-dbus-proxy xkb-data fonts-dejavu-core adwaita-icon-theme
./build-target.sh linux amd64
```

The build packages shared libraries, WebKit subprocesses, GIO and image-loader
modules, schemas, keyboard layouts, fonts, sandbox utilities, and Mesa's software
renderer. The destination machine does not need these packages installed. glibc
and its loader remain supplied by the destination OS. Build with older compatible
libraries to avoid requiring a newer glibc on the destination.

To use an isolated dependency root instead of installing the packages globally:

```sh
WEBVIEW_SYSROOT=/path/to/root ./build-target.sh linux amd64
```

That root must contain all required headers, libraries, executables, data, and
pkg-config metadata, including transitive dependencies.

## Windows desktop

Cross-compiling from Linux requires MinGW-w64 C and C++ compilers for the target
architecture. For amd64 on Debian-based systems:

```sh
sudo apt-get install gcc-mingw-w64-x86-64 g++-mingw-w64-x86-64 cmake make python3 cabextract
./build-target.sh windows amd64
```

CAB extraction needs `cabextract` or 7-Zip on Linux. Native Windows builds can use
the OS-provided `expand.exe`.

The defaults are `x86_64-w64-mingw32-gcc` and `x86_64-w64-mingw32-g++`.
`WINDOWS_CC` and `WINDOWS_CXX` select alternative compiler paths. Other
architectures require their matching cross-compilers; an amd64 compiler cannot
build an arm64 helper.

For a native Windows build, install Go, Bash, Python 3, CMake, Make, and a compatible
MinGW-w64 C/C++ toolchain. Put `go`, `python3`, `gcc`, and `g++` on `PATH`, or set `CC` and
`CXX` to the native compiler paths. Run `./build-target.sh windows amd64` from Bash.
The pinned binding requires C++14 support. Its SDK headers are included with the
Go dependency; the build downloads and bundles the matching Microsoft loader DLL.

The script automatically downloads, verifies, extracts, and bundles the pinned
[Microsoft Fixed Version WebView2 runtime](https://developer.microsoft.com/en-us/microsoft-edge/webview2/)
for the target architecture. Version, Microsoft download URLs, sizes, and SHA-256
checksums are recorded in `desktop/webview/windows/runtime.json`. The matching
Microsoft loader DLL is fetched from its pinned NuGet SDK package and bundled
with its license. Download and extraction failures stop the build instead of
producing an executable that needs an installed runtime.

Downloaded archives and extracted runtimes are cached under
`target/cache/webview2/`, which is ignored by Git. Subsequent builds reuse the
cache without downloading again. Preserve this directory for offline builds;
remove it to fetch the pinned runtime again. Runtime updates require updating the
manifest and rebuilding the executable.

To use a different or already extracted runtime, override the automatic download:

```sh
WEBVIEW2_RUNTIME_DIR=/path/to/extracted/webview2 ./build-target.sh windows amd64
```

The directory must contain `msedgewebview2.exe` and all its supporting files for
the target architecture. This override avoids the CAB download and extraction
tool. The pinned Microsoft loader must still be available in the build cache or
downloaded on the first build.

## macOS desktop

Build on a Mac matching the target architecture. Install Go, Python 3, CMake, and the
Xcode command-line tools, which provide Clang and the macOS SDK:

```sh
xcode-select --install
./build-target.sh darwin arm64   # Apple Silicon
# Use darwin amd64 on an Intel Mac.
```

The helper links to Cocoa and WebKit supplied by macOS. There is no GTK/WebKitGTK
bundle to install. macOS desktop builds have not yet been exercised for this
project; no additional minimum OS version beyond toolchain/framework compatibility
has been established.

## Build commands and outputs

```sh
./build-target.sh linux amd64          # One desktop target
./build-target.sh windows amd64
./build.sh                            # Linux amd64, then Windows amd64
./build.sh --headless                 # Both targets, HTTP only
./build-target.sh windows arm64 --headless
```

Run the two-target wrapper on Linux amd64. Desktop builds require the Linux
build dependencies and Windows cross-compilers on the build machine.
`WEBVIEW_SYSROOT` applies only to Linux; `WEBVIEW2_RUNTIME_DIR` applies only to Windows.

Outputs are named `target/shelfmark-<os>-<arch>` (`.exe` on Windows), with a
companion `target/shelfmark-<os>-<arch>-licenses.tar.gz` notices archive. Each build
replaces that target's previous executable. Only the final executable needs to be
copied to the destination for running Shelfmark; license/source distribution
obligations are described in [THIRD_PARTY.md](THIRD_PARTY.md). The helper and runtime
archive are temporary build artifacts. There is no frontend build step or Node.js
dependency.

Plain `go build ./cmd/shelfmark` also creates an HTTP-only executable. See
[Desktop runtime packaging](docs/desktop.md) for bundle contents, implementation
limitations, and redistribution requirements.

## Development

```sh
go build ./...
go vet ./...
```

Frontend HTML, CSS, and JavaScript live in `internal/webui` and are embedded with
`go:embed`. There is no JavaScript framework, package manager, or frontend build
step. Rebuild the executable after changing assets.

See [Architecture](docs/architecture.md) for package responsibilities and
[UX improvements](docs/ux.md) for planned interface changes.
