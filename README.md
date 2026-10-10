[![Build](https://github.com/Nejaa/ShelfMark/actions/workflows/ci.yml/badge.svg)](https://github.com/Nejaa/ShelfMark/actions/workflows/ci.yml)

# Shelfmark

Shelfmark manages metadata for a local ebook collection. Scan a folder, find matching
catalog records, edit metadata, and review selected changes before writing them to
your books. Drafts are stored in SQLite and survive restarts.

The interface is a single page web application embedded in the Go executable.
Shelfmark can serve it in a browser or display it in an optional desktop window.
It does not require Calibre.

## Features

- Read and edit metadata in EPUB, FB2, CBZ, and PDF files.
- Search Google Books, Open Library, and Internet Archive for matching records.
- Review metadata suggestions and keep drafts between sessions.
- Sort the library by series, volume, and title; filter by filename, title, or author.
- Inspect local text and use optional OCR to find ISBNs.
- Use the same interface in a desktop window or a browser.

## Getting started

Download the executable for your system from [GitHub Releases](https://github.com/Nejaa/ShelfMark/releases)
and put it in a writable folder. Release downloads are available for **Linux x64
and Windows x64**. See [Downloads](#downloads) for the files included in each release.
Experimental macOS source builds are described in [BUILD.md](BUILD.md#macos-desktop).
Check the [runtime requirements](#runtime-requirements) before downloading.

On Windows, open `shelfmark-windows-amd64.exe`.
On Linux:

```sh
chmod +x shelfmark-linux-amd64
./shelfmark-linux-amd64
```

The executable includes the HTTP server, UI, desktop helper, and Tesseract OCR
with English and French language models. Linux releases bundle GTK/WebKitGTK;
Windows releases bundle a Fixed Version WebView2 runtime. Shelfmark starts its
HTTP server first and attempts to open a desktop window. If the window cannot
start, the server continues running so you can use a browser. For source builds,
see [BUILD.md](BUILD.md).

Open **http://127.0.0.1:8766**. Choose a folder on the computer running Shelfmark and
scan it. **Back up your books before saving changes: Shelfmark does not create
backup copies.**

1. Select a book and find catalog matches, or edit its metadata directly.
   Searches use the title, subtitle, original title and author, plus ISBNs when
   available. Sparse metadata falls back to a cleaned filename. You can edit the
   terms to run a custom search.
2. Review the proposed metadata and the explanation for each field. Check the
   title, authors, series, volume, and edition details. Select only the values
   you want to apply; uncertain or conflicting suggestions start unchecked.
3. Choose the fields to apply to a draft. This does not change the file.
4. Open **Review & save drafts**, select books and fields, and save the updates.
   To abandon staged edits, use **Discard draft** for one book, or
   **Discard selected drafts** for several. Discarding removes the whole draft
   without modifying the ebook file; closing the review keeps drafts for later.

Matching tries ISBN lookups and title/author variations, using cleaned filenames
when metadata is sparse. Results are ranked using title similarity, authors,
language, and ISBN agreement. Suggestions include explanations for individual
fields; uncertain or conflicting values start unchecked. Catalog records can be
incomplete or describe a different edition, so review them before applying changes.
Matching never modifies files automatically.

Choosing a folder in the folder picker starts a scan. Background matching searches
the selected folder and its scanned subfolders, filling the cache without applying
changes. Manual searches take priority. You can stop background matching from the
library; closing a browser tab does not stop it. A new scan or shutting down
Shelfmark cancels it.

## Downloads

Download release files from [GitHub Releases](https://github.com/Nejaa/ShelfMark/releases).
Choose the executable for your system, or the Docker archive for a container.
The checksums and source archives are accompanying downloads; they are not needed
to launch Shelfmark.

The release assets contain:

| File | Contents |
| --- | --- |
| `shelfmark-linux-amd64` | Linux desktop executable with bundled OCR |
| `shelfmark-windows-amd64.exe` | Windows desktop executable with bundled OCR |
| `shelfmark-linux-amd64-licenses.tar.gz` | Linux executable's dependency notices |
| `shelfmark-windows-amd64-licenses.tar.gz` | Windows executable's dependency notices |
| `shelfmark-docker-linux-amd64.tar.gz` | Compressed `docker save` archive for `docker load` |
| `shelfmark-source.tar.gz` | Project source from the release tag |
| `shelfmark-third-party-sources.tar.gz` | Corresponding dependency sources and models |
| `SHA256SUMS` | SHA-256 checksums for the release assets |

## Running

### Runtime requirements

Use an executable matching the operating system and CPU architecture. Go, Python,
a C/C++ compiler, and a separate SQLite installation are not needed to run it.

| System | HTTP-only server | Additional requirements for a desktop window |
| --- | --- | --- |
| Linux x64 release | A 64-bit Linux system; no GTK or WebKit installation. Bundled OCR requires glibc 2.36 or newer. | X11 or Wayland, glibc 2.36 or newer with its ELF loader, and kernel support for WebKit's sandbox. GTK, WebKitGTK, fonts, and software rendering libraries are bundled. |
| Windows x64 release | Windows 10/11 or a compatible Windows Server version | A graphical desktop session. Desktop builds bundle a compatible WebView2 runtime; no separate installation is needed. Current WebView2 targets Windows 10/11 and supported Windows Server editions; see [Microsoft's OS requirements](https://learn.microsoft.com/en-us/deployedge/microsoft-edge-supported-operating-systems). |

Shelfmark needs a writable data directory, permission to read the library, and an
available TCP listen port. Saving metadata also needs permission to write book
files and create temporary files in their folders. A modern browser, local or on
another device, is needed to use the HTTP interface.

Desktop mode needs enough writable temporary disk space to extract the bundled
runtime, and permission to execute the extracted helpers. On Linux, the temporary
filesystem must not be mounted `noexec`; use a short `TMPDIR` if path relocation
fails. The desktop and OCR helpers require glibc 2.36 or newer. WSL
needs WSLg or another display server for a Linux window. HTTP-only operation does
not need a display.

On Windows, Shelfmark extracts the bundled Fixed Version WebView2 runtime onto
a local filesystem. An installed WebView2 runtime is not required. On first
desktop launch, read and agree to Microsoft's separate runtime terms in the
native confirmation dialog. Declining leaves HTTP mode available. The terms are
served at `/api/webview2-license`; acceptance is recorded beside the executable.
The runtime does not update itself; install a newer Shelfmark version to update it.

Internet access is needed for enabled catalog searches and downloading remote
covers, but not for scanning or editing local books.

Release executables include Tesseract and its dependencies, with English and
French language models. OCR works offline without a separate installation,
including in headless mode. The Linux OCR helper needs compatible glibc and its
loader; Windows uses OS system libraries.

On first launch the bundled models are installed into `tessdata/` beside the
executable. Existing nonempty model files are preserved. To restore a damaged
model, remove its `eng.traineddata` or `fra.traineddata` file and restart. The
private OCR helper is extracted to a temporary directory and removed on shutdown;
that directory must permit execution. OCR failures leave the rest of the app
available.

### Command-line options

| Flag | Default | Purpose |
| --- | --- | --- |
| `-headless` | `false` | Serve HTTP without attempting a desktop window |
| `-listen` | `127.0.0.1:8766` | HTTP listen address |
| `-log-level` | Saved setting (`info` on first launch) | Override the initial severity: `debug`, `info`, `warn`, or `error` |
| `-data-dir` | Directory containing the executable | SQLite storage directory |

To access the interface from another device on a trusted network:

```sh
./shelfmark-linux-amd64 -headless -listen 0.0.0.0:8766 -data-dir /path/to/shelfmark-data
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

The Windows release executable opens no command console when launched from
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
./shelfmark-linux-amd64 -log-level debug
```

Change the minimum level in **Settings → Logging** and save to apply it immediately.
The choice is stored in SQLite and restored on launch. `-log-level` overrides the
saved level at startup; Settings shows the active level and can change it for the
running session. The flag alone does not change the saved preference.

The selected level includes all higher severities. Flag help and crash reports
remain available independently of the filter. OCR engine or language-data failures
produce a warning with troubleshooting steps when OCR is enabled. Settings also
shows a red warning if OCR is selected but unavailable on the server.

Log records can contain local file paths. Debug logging also includes search terms
and candidate titles. API keys, request bodies, book text, and OCR output are not
logged.

Because the Windows executable uses the GUI subsystem, `cmd.exe` can return to its
prompt while Shelfmark is running. Use `start /wait "" shelfmark-windows-amd64.exe`
if you want the shell to wait for it, or run it with `Start-Process -Wait` in
PowerShell. A windowless launch can be stopped through Task Manager if no console
is attached.

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
with bundled OCR and no desktop window. Its base image is `debian:bookworm-slim`.
`/data` holds SQLite, logs, and OCR models.
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
models in release executables. Turning off content inspection
also disables OCR for searches. OCR is used for opening images in EPUB and CBZ;
PDF matching reads the text layer and does not OCR scanned PDF pages. Custom
search terms bypass local content inspection.

Selecting a book displays any matching, unexpired cached results.
A cache miss does not start a search; use **Find matches** to query the catalogs.
Cached searches expire after seven days by default; set the duration to zero to
disable caching. The cache holds at most 500 search entries. **Find matches** retries a
full search when the cached result is empty. Check **Ignore cache** beside the
button to force a fresh search even when cached matches exist. Successful fresh
searches update the cache. Background matching can reuse empty results. Provider
failures are shown separately from empty results. Failed searches with
no matches are not cached. Matching scores are heuristics;
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

## Project documentation

- [Building from source](BUILD.md): prerequisites, native builds, and container builds.
- [Maintainer guide](MAINTAINER.md): CI, releases, and dependency updates.
- [Architecture](docs/architecture.md): package responsibilities.

## License

Shelfmark's own code is licensed under the [MIT License](LICENSE), permitting
commercial and private use, modification, and redistribution with the copyright
and license notice retained. Dependencies keep their original terms, including
the separately licensed Windows WebView2 runtime.

Release executables include original notices, available from the **Licenses** link in
the UI or `/api/licenses`. Releases publish notices and corresponding sources
alongside their binaries. See [Third-party software](THIRD_PARTY.md) for component
licenses, native rebuild instructions, and redistribution details.
