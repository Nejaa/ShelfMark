[![Build](https://github.com/Nejaa/ShelfMark/actions/workflows/ci.yml/badge.svg)](https://github.com/Nejaa/ShelfMark/actions/workflows/ci.yml)

# Shelfmark

Shelfmark manages metadata for a local ebook collection. Scan a folder, find matching
catalog records, edit metadata, and review selected changes before writing them to
your books. Drafts are stored in SQLite and survive restarts.

The interface is a single page web application embedded in the Go executable.
Shelfmark can serve it in a browser or display it in an optional desktop window.
It does not require Calibre.

## Getting started

Download the executable for your system from [GitHub Releases](https://github.com/Nejaa/ShelfMark/releases)
and put it in a writable folder. On Windows, open `shelfmark-windows-amd64.exe`.
On Linux:

```sh
chmod +x shelfmark-linux-amd64
./shelfmark-linux-amd64
```

To build from source, install the Go version specified in [go.mod](go.mod), Python 3, and the native
build dependencies in [Building](#building). Build and run:

```sh
./build-target.sh linux amd64
./target/shelfmark-linux-amd64
```

The desktop build produces one executable for the requested platform and
architecture. It contains the HTTP server, UI, native GUI helper, and Tesseract OCR
with English and French language models. Linux builds
also embed a private GTK/WebKitGTK runtime; Windows builds embed a Fixed Version
WebView2 runtime. If the window cannot initialize, the
server continues running and the interface remains available in a browser.

For an HTTP-only executable without native build dependencies:

```sh
CGO_ENABLED=0 go build -o shelfmark ./cmd/shelfmark
./shelfmark -headless
```

Open **http://127.0.0.1:8766**. Choose a folder on the computer running Shelfmark and
scan it. Supported files are EPUB, FB2, CBZ, and PDF.

1. Select a book and find catalog matches, or edit its metadata directly.
   Searches use the title, subtitle, original title and author, plus ISBNs when
   available. Sparse metadata falls back to a cleaned filename. You can edit the
   terms to run a custom search.
2. Review the proposed metadata and its field-by-field reasoning. Series and
   volume belong in their own fields; specific subtitles can become the main title
   when the old title is just a series label. Useful local values are retained
   when catalogs describe another language or an uncertain edition. Conflicting
   and weakly inferred values start unchecked.
3. Choose the fields to apply to a draft. This does not change the file.
4. Open **Review & save drafts**, select books and fields, and save the updates.

Automatic matching searches ISBNs independently, then tries title/author variants.
Sources without a strong match are searched with fewer constraints. Results are
ranked using accent-insensitive edit distance, word overlap, author names and
initials, language agreement, and ISBN-10/ISBN-13 equivalence. Numbered titles and
conflicting authors receive lower scores. These scores order suggestions; matching
never changes a file automatically. Open Library work records may combine details
from multiple editions, so review publication fields before applying them.

ISBN lookups in Open Library also retrieve the specific edition record, which
can supply a more useful title than the work record. Explicit contributor roles
separate authors from translators. Title clues from records sharing an ISBN can
corroborate a proposal; the editor shows the reasoning and requires selection of
uncertain values. Search and reconciliation run in Go.

Folder selection starts a scan automatically. The library can be filtered by
filename, title, or author. Background matching searches books in the selected
folder and its scanned subfolders; it populates the search cache without applying
changes. Manual searches take priority, and background matching can be stopped. Go owns the background job; closing a browser tab does
not stop it. A new scan or shutting down Shelfmark cancels it.

## Running

### Runtime requirements

Use an executable matching the operating system and CPU architecture. Go, Python,
a C/C++ compiler, and a separate SQLite installation are not needed to run it.

| System | HTTP-only server | Additional requirements for a desktop window |
| --- | --- | --- |
| Linux | A Linux version supported by the Go toolchain used to build the executable; no GTK or WebKit installation required | X11 or Wayland, glibc and its ELF loader compatible with the bundled libraries, and kernel support for WebKit's sandbox. GTK, WebKitGTK, fonts, and Mesa software rendering are bundled. |
| Windows | Windows supported by the Go toolchain used to build the executable | A graphical desktop session. Desktop builds bundle a compatible WebView2 runtime; no separate installation is needed. Current WebView2 targets Windows 10/11 and supported Windows Server editions; see [Microsoft's OS requirements](https://learn.microsoft.com/en-us/deployedge/microsoft-edge-supported-operating-systems). |
| macOS | macOS supported by the Go toolchain used to build the executable | A graphical desktop session and the system Cocoa/WebKit frameworks. No separate browser engine installation is needed. |

All builds need a writable data directory, permission to read the library, and an
available TCP listen port. Saving metadata also needs permission to write book
files and create temporary files in their folders. A modern browser, local or on
another device, is needed to use the HTTP interface.

Desktop builds need enough writable temporary disk space to extract their bundled
runtime, and permission to execute the extracted helper. On Linux, the temporary
filesystem must not be mounted `noexec`; use a short `TMPDIR` if path relocation
fails. The Linux desktop bundle has been exercised on Debian 11 amd64 with glibc
2.31 and on WSLg. Compatibility with other systems depends on the libraries used
when building the bundle. WSL needs WSLg or another working display server to open
a Linux window; HTTP-only operation does not need one.

Windows desktop builds include a Fixed Version WebView2 runtime, extracted onto a
local filesystem at startup. Shelfmark grants its AppContainer renderer groups
read/execute permissions on the extracted runtime, including the permissions
required on Windows 10 for Fixed Version 120 and later. See
[Microsoft's distribution guide](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution#the-fixed-version-runtime-distribution-mode).
An installed WebView2 runtime is not required. On first desktop launch, read and
agree to Microsoft's separate runtime terms in the native confirmation dialog.
Declining leaves HTTP mode available. The terms are served at
`/api/webview2-license`; acceptance is recorded beside the executable.
If a desktop runtime or display cannot initialize, Shelfmark continues over HTTP.

Internet access is needed for enabled catalog searches and downloading remote
covers, but not for scanning or editing local books.

Script-built executables include Tesseract and its dependencies, with English and
French language models. OCR works offline without a separate installation,
including in headless mode. The Linux OCR helper needs compatible glibc and its
loader; Windows uses only OS system libraries. macOS uses system libraries too.

On first launch the bundled models are installed into `tessdata/` beside the
executable. Existing nonempty model files are preserved. To restore a damaged
model, remove its `eng.traineddata` or `fra.traineddata` file and restart. The
private OCR helper is extracted to a temporary directory and removed on shutdown;
that directory must permit execution. OCR failures leave the rest of the app
available.

Direct `go build` executables do not bundle native runtimes. They can use
`tesseract` on the server's `PATH`, with English or French language data installed.

### Command-line options

| Flag | Default | Purpose |
| --- | --- | --- |
| `-headless` | `false` | Serve HTTP without attempting a desktop window |
| `-listen` | `127.0.0.1:8766` | HTTP listen address |
| `-log-level` | Saved setting (`info` on first launch) | Override the initial severity: `debug`, `info`, `warn`, or `error` |
| `-data-dir` | Directory containing the executable | SQLite storage directory |

To access the interface from another device on a trusted network:

```sh
./shelfmark -headless -listen 0.0.0.0:8766 -data-dir /path/to/shelfmark-data
```

Use the server's address from the other device. Folder paths always refer to the
server's filesystem. Shelfmark has no user authentication; its API can browse
folders and update book files. Keep the default loopback binding unless you trust
the devices that can reach it. For remote access, use an authenticated reverse
proxy or an SSH tunnel.

The HTTP server starts before a desktop window is attempted. If a window is
unavailable, the server stays running. Stop the server with Ctrl+C. Closing an
opened desktop window stops Shelfmark.

### Data and logs

By default, Shelfmark creates `shelfmark.sqlite3` and `shelfmark.log` beside its
executable, regardless of the directory it was launched from. Keep the executable
in a writable folder. `-data-dir` changes the database location; the log file
stays beside the executable. Logs are appended across launches and may be removed
when Shelfmark is stopped.

Windows builds made by the scripts open no command console when launched from
Explorer. When launched from a terminal, Shelfmark reuses that console and also
writes its logs there. Redirected terminal output is preserved. Native GUI helper
diagnostics are included in `shelfmark.log`.

Log records include their severity and structured details. The default `info`
level shows startup, scans, searches, draft saves, and feature availability.
`warn` includes recoverable failures and missing optional dependencies; `error`
includes failed saves and fatal startup errors. Enable `debug` for HTTP timings,
cache activity, generated search plans, catalog calls, candidate ranking, metadata
processing, and OCR progress:

```sh
./shelfmark -log-level debug
```

Change the minimum level in **Settings → Logging** and save to apply it immediately.
The choice is stored in SQLite and restored on launch. `-log-level` overrides the
saved level at startup; Settings shows the active level and can change it for the
running session. The flag alone does not change the saved preference.

The selected level includes all higher severities. Flag help and crash reports
remain available independently of the filter. OCR engine or language-data failures
produce an actionable warning when OCR is enabled. Settings also shows a red
warning when the selected OCR engine is unavailable on the server. Repeated
availability warnings are limited to once per minute; successful OCR discovery
is cached for the session. Failed language discovery is retried after a minute, avoiding a failing
subprocess for every image.
Log records can contain local file paths. Debug logging also includes search terms
and candidate titles. API keys, request bodies, book text, and OCR output are not
logged.

Because the Windows executable uses the GUI subsystem, `cmd.exe` can return to its
prompt while Shelfmark is running. Use `start /wait "" shelfmark-windows-amd64.exe`
if you want the shell to wait for it, or run it with `Start-Process -Wait` in
PowerShell. A windowless launch can be stopped through Task Manager if no console
is attached.

Earlier versions stored data in the user configuration directory under
`Shelfmark`. Existing data is not moved automatically: pass that directory with
`-data-dir`, or move the database and any SQLite sidecar files beside the executable
while Shelfmark is stopped.

## Docker

Download `shelfmark-docker-linux-amd64.tar.gz` from a GitHub release, then import
it into Docker. The image is distributed as a file; no registry account is needed.
Replace the example version with the release you downloaded:

```sh
docker load --input shelfmark-docker-linux-amd64.tar.gz
docker run --rm --name shelfmark \
  -p 127.0.0.1:8766:8766 \
  --mount type=volume,source=shelfmark-data,target=/data \
  --mount type=bind,source=/absolute/path/to/books,target=/books \
  shelfmark:v0.1.0
```

Open <http://localhost:8766> and scan `/books`. The container runs the HTTP server
with bundled OCR and no desktop window. `/data` holds SQLite, logs, and OCR models.
The books mount needs write access for saving metadata; add `readonly` if you only
want to scan. The process uses UID/GID `10001:10001`: grant that identity access to
bind-mounted books and, if using a bind mount for `/data`, write access there.
A Docker named data volume is initialized with the image's ownership.

On Windows with Docker Desktop's Linux containers, use a Windows source path for
the books mount. Paths entered in Shelfmark still refer to the container, such as
`/books`, rather than a host drive letter. The image supports Linux/amd64; other
architectures need a corresponding build or Docker's platform emulation.

Additional flags can be appended after the image name, for example
`-log-level debug`. Keep the loopback port mapping unless remote access is intended;
the application does not provide authentication. Stop it with `docker stop shelfmark`.

## Building

### Container builds

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
If public image pulls fail with HTTP 401, refresh the Docker credentials used by
your CLI; these images do not require a registry account.

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

### GitHub builds and releases

GitHub Actions compiles and runs Go static analysis on pushes to `develop`/`main`
and pull requests. The release workflow builds in Docker and publishes Linux and
Windows amd64 executables, their notices, a downloadable Docker image, project and
third-party sources, and SHA-256 checksums. It does not push to a container registry.

After committing these workflows, push a version tag to publish a release:

```sh
git tag -a v0.1.0 -m "Shelfmark 0.1.0"
git push origin v0.1.0
```

A tag such as `v0.1.0-rc.1` creates a prerelease. Manual runs of the **Release**
workflow require an existing tag and create a draft for review. Automatic tag
runs publish only after all assets and matching sources have uploaded. Published
versions are not overwritten; use a new tag. An interrupted upload can leave a
draft, which can be retried. The workflow uses the repository's `GITHUB_TOKEN`
with release write permission; no additional secrets are required. GitHub Actions
must be enabled and repository policies must allow the job's write permission.

Large third-party source archives are split into numbered parts. Concatenate them
in filename order, then extract the reconstructed archive. `SHA256SUMS` covers
all assets, including individual parts. Keep matching sources and notices with
the release when redistributing binaries; see [Third-party software](THIRD_PARTY.md).


### Common requirements

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

### Linux desktop

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

### Windows desktop

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
with its license. Download and
extraction failures stop the build instead of producing an executable that needs
an installed runtime.

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
the target architecture. This override avoids the CAB download and extraction tool. The pinned Microsoft
loader must still be available in the build cache or downloaded on the first build.

### macOS desktop

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

### Build commands and outputs

```sh
./build-target.sh linux amd64          # One desktop target
./build-target.sh windows amd64
./build.sh                            # Linux amd64, then Windows amd64
./build.sh --headless                 # Both targets, HTTP only
./build-target.sh windows arm64 --headless
```

The two-target desktop wrapper requires the Linux build dependencies and Windows
cross-compilers on its build machine. `WEBVIEW_SYSROOT` applies only to Linux;
`WEBVIEW2_RUNTIME_DIR` applies only to Windows.

Outputs are named `target/shelfmark-<os>-<arch>` (`.exe` on Windows). Each build
replaces that target's previous executable. Only the final executable needs to be
copied to the destination for running Shelfmark; license/source distribution
obligations are described in [THIRD_PARTY.md](THIRD_PARTY.md). The helper and runtime
archive are temporary build artifacts. There is no frontend build step or Node.js dependency.

Plain `go build ./cmd/shelfmark` also creates an HTTP-only executable. See
[Desktop runtime packaging](docs/desktop.md) for bundle contents, implementation
limitations, and redistribution requirements.

## Settings

Settings are grouped by purpose and stored on the server. Changes affect future
operations; scanning preferences require a new scan.

| Category | Controls |
| --- | --- |
| Library | Last folder, recursive scanning, hidden files and folders |
| Catalogs | Enable Google Books, Open Library, and Internet Archive individually; optional Google Books API key |
| Matching | Local content inspection, optional OCR, maximum candidates, cache duration, background search delay |
| Logging | Minimum severity (debug, info, warning, error), applied immediately when saved |

Catalogs receive search terms and discovered ISBNs. Book text is inspected locally
and is not uploaded. OCR of opening images uses the bundled English and French
models in script-built executables. Turning off content inspection
also disables OCR for searches.

Cached searches expire after seven days by default; set the duration to zero to
disable caching. The cache holds at most 500 results. **Find matches** retries a
full search when the cached result is empty. Check **Ignore cache** beside the
button to force a fresh search even when cached matches exist. Successful fresh
searches update the cache. Background matching can reuse empty results. Provider failures are shown
separately from empty results and are not cached. Matching scores are heuristics;
verify the edition before using a record. Open Library results can describe a
work rather than the specific edition of your file.

API keys are stored unencrypted in the local database and are not returned to the
browser. Leave the key field blank to keep a saved key, or use the remove control
to delete it from the active settings.

## Files and data

| Format | Metadata storage | Cover replacement |
| --- | --- | --- |
| EPUB | Package metadata, including additional Calibre properties | Supported |
| FB2 | Description metadata and additional Shelfmark properties | Not supported |
| CBZ | `ComicInfo.xml` | Not supported; archive pages remain intact |
| PDF | Document properties | Not supported |

Additional properties may not be displayed by every ebook reader. Some formats
store fewer details than others: FB2 has one language, for example. DRM-protected,
encrypted, or malformed books may not be editable.

Selected updates replace the original file through a temporary file in the same
folder. File permissions are preserved. **Shelfmark does not create backup
copies.** Back up your collection before editing it. A save checks whether the
file or draft changed since the review was opened and asks for a new review when
needed. Saving several books is not a transaction: failures are reported per
book, and earlier successful updates remain saved.

`shelfmark.sqlite3` contains settings, catalog caches, and drafts. SQLite may also
create `-wal` and `-shm` files while the app is running. Stop Shelfmark before
copying its data directory. Original book files are stored separately. Existing
databases are migrated automatically when opened; preserve a copy before using a
newer version if you need to return to an older executable.

## Development

```sh
go build ./...
go vet ./...
```

Build commands and platform prerequisites are listed in [Building](#building).

Frontend HTML, CSS, and JavaScript live in `internal/webui` and are embedded with
`go:embed`. There is no JavaScript framework, package manager, or frontend build
step. Rebuild the executable after changing assets.

See [Architecture](docs/architecture.md) for package responsibilities and
[UX improvements](docs/ux.md) for proposed extensions.

## License

Shelfmark's own code is licensed under the [MIT License](LICENSE), permitting
commercial and private use, modification, and redistribution with the copyright
and license notice retained. Dependencies keep their original terms, including
the separately licensed Windows WebView2 runtime.

Scripted builds include original notices, available from the **Licenses** link in
the UI or `/api/licenses`. Releases publish notices and corresponding sources
alongside their binaries. See [Third-party software](THIRD_PARTY.md) for component
licenses, native rebuild instructions, and redistribution details.
