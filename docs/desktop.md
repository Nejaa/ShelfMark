# Desktop runtime packaging

`./build-target.sh <os> [arch]` builds the native helper, packages its runtime, and embeds the archive
in a Go executable built with `CGO_ENABLED=0`. Only the final executable needs to
be copied to the destination. The helper and archive are temporary build artifacts removed after the build.
A Go build overlay provides the archive to `go:embed` without writing it into the
source tree. `./build.sh` calls the target script for Linux amd64 and Windows
amd64 in order.

The helper is isolated because the operating system may reject a dynamically
linked program before its entry point runs. Keeping the server independent of
native libraries allows HTTP startup even in that case.

## Linux bundle

The packager gathers the helper's ELF dependency closure and additional libraries
loaded dynamically. It includes GTK 3, WebKitGTK 4.0, JavaScriptCore, WebKit's
network and web subprocesses, the injected bundle, GIO TLS modules, image loaders,
GSettings schemas, keyboard layouts, fonts, icons, sandbox utilities, and Mesa's
software renderer. It leaves glibc and its ELF loader on the host.

Runtime directories are private temporary directories. Set a short `TMPDIR` if
your default temporary directory is unusually long: binary string relocation
cannot grow the original ELF strings. The filesystem must allow execution.

Release WebKit builds ignore the developer `WEBKIT_EXEC_PATH` environment
variable ([upstream subprocess lookup](https://github.com/WebKit/WebKit/blob/main/Source/WebKit/Shared/glib/ProcessExecutablePathGLib.cpp)). `relocations.json` therefore records complete compiled paths. At
extraction, the parent replaces them with shorter private paths, NUL-padding the
remaining space. A missing or oversized relocation causes graceful fallback.
Unknown WebKit layouts fail at packaging time rather than producing a bundle that
silently uses system WebKit subprocesses.

Mesa software rendering avoids dependence on vendor-specific GPU drivers. The
helper prefers X11 under WSLg because the bundled runtime can produce a blank
surface through its Wayland backend. Wayland remains a fallback when X11 is
unavailable; an explicit `GDK_BACKEND` environment variable overrides that choice.
Other Linux desktops retain GTK's default backend selection.

The application still needs the host display server, compatible glibc, and kernel
features required by the WebKit sandbox. Sandboxing is not disabled to force a
window to open. Linux packaging currently targets Debian-style multiarch layouts
and the pinned binding's WebKitGTK 4.0 ABI.

## Other platforms

The process boundary and embedding code are shared. Windows builds automatically
bundle a pinned Microsoft Fixed Version WebView2 runtime. The downloader verifies
its size and SHA-256 checksum before extraction and caches it under
`target/cache/webview2/`. The tracked `desktop/webview/windows/runtime.json`
manifest records the version, Microsoft URLs, sizes, and hashes for amd64, arm64,
and 386, plus the matching Microsoft WebView2 SDK loader package and DLL hashes.
It must be updated to incorporate runtime security updates. The binding is built
with its internal loader disabled, so the bundled Microsoft loader resolves the
explicit runtime folder instead of selecting an installed system runtime.

Linux build hosts need `cabextract` or 7-Zip for the first extraction. Native
Windows hosts can use `expand.exe`. Supplying `WEBVIEW2_RUNTIME_DIR` overrides the
download with an extracted runtime matching the build architecture. All runtime
files and notices are preserved. At launch, inherited read/execute ACL entries
allow WebView2's AppContainer renderer to access the temporary runtime; existing
permissions are retained and the data directory is unaffected. See
[Microsoft's distribution guide](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution).
macOS uses system WebKit and requires no GTK bundle.

Windows helpers can be cross-compiled on Linux with MinGW-w64. The build sets
`GOOS`, `GOARCH`, `CGO_ENABLED`, `CC`, and `CXX`; a small SDK compatibility header
supplies the case-sensitive `EventToken.h` include used by the pinned binding.
The packager selects the requested OS rather than the build host and checks
Windows helper and supplied runtime PE architectures. Linux and macOS packaging
currently requires a host matching the target OS and architecture. Platform
signing and notarization are separate release steps.

## Dependency inventory and redistribution

The archive includes `runtime-manifest.json` with original file hashes and source
paths, `runtime-packages.json` with host and dependency-root package versions,
and `licenses/` with distribution copyright notices and common license texts.
Webview's license is included separately. Some notices describe packages outside
the final dependency closure because the build preserves the distribution's
available notice set.

These are third-party libraries with individual licenses, including LGPL
components ([GNU licensing FAQ](https://www.gnu.org/licenses/gpl-faq.html#LGPLStaticVsDynamic)). Before publishing a binary, retain those notices, obtain the exact
corresponding sources for bundled packages, and fulfill the source and replacement
requirements applicable to each component. Package versions are recorded to make
that work reproducible. Debian sources can be obtained with `apt-get source`
using the matching repository and recorded source version. Preserve those source
archives alongside release artifacts. The executable contains notices and runtime
binaries; it does not include corresponding source archives.

Relocation modifies compiled paths, which is recorded in `relocations.json` and
implemented in `internal/desktop/desktop.go`. Rebuilding the runtime from modified
libraries and re-embedding it is supported by the same packaging script. Bundled
browser libraries need maintenance: rebuilding against updated distribution
packages is required to incorporate their security fixes.
