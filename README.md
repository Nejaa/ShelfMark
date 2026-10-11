[![Build](https://github.com/Nejaa/ShelfMark/actions/workflows/ci.yml/badge.svg)](https://github.com/Nejaa/ShelfMark/actions/workflows/ci.yml)

# Shelfmark

Shelfmark manages metadata for a local ebook collection. Scan a folder, find matching
catalog records, edit metadata, and review selected changes before writing them to
your books. Drafts are stored in SQLite and survive restarts.

The web interface is embedded in the executable and can be used in a browser or
desktop window. 

Shelfmark does not require Calibre.

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
Windows releases bundle a Fixed Version WebView2 runtime. Shelfmark starts in
desktop mode by default. Use `-headless` to run the HTTP server without opening
a desktop window. If desktop mode is unavailable, use the browser interface.
For source builds, see [BUILD.md](BUILD.md).

Open **http://127.0.0.1:8766**. Choose a folder on the computer running Shelfmark and
scan it. **Back up your books before saving changes: Shelfmark does not create
backup copies.**

1. Select a book and search for catalog matches, or edit its metadata directly.
   Search terms come from available book metadata and can be edited.
2. Review the proposed metadata and the explanation for each field. Check the
   title, authors, series, volume, and edition details. Select only the values
   you want to apply; uncertain or conflicting suggestions start unchecked.
3. Choose the fields to apply to a draft. This does not change the file.
4. Open **Review & save drafts**, select books and fields, and save the updates.
   To abandon staged edits, use **Discard draft** for one book, or
   **Discard selected drafts** for several. Discarding removes the whole draft
   without modifying the ebook file; closing the review keeps drafts for later.

Catalog records can be incomplete or describe a different edition. Review
suggestions before applying them. Matching does not modify book files.

Selecting a folder starts a scan. Background matching searches scanned books and
stores results without applying changes. It continues if the browser tab closes
and stops when cancelled, a new scan starts, or Shelfmark shuts down.

## Downloads

Download release files from [GitHub Releases](https://github.com/Nejaa/ShelfMark/releases).
Choose the Linux or Windows executable, or the Docker archive. Releases also
include checksums, license notices, and source archives.

## Running

### Runtime requirements

Release executables are available for Linux x64 and Windows x64. Go, Python, a
C/C++ compiler, and a separate SQLite installation are not required.

| System | Requirements |
| --- | --- |
| Linux x64 | 64-bit Linux. Bundled desktop and OCR helpers require glibc 2.36 or newer. Desktop mode requires X11 or Wayland and kernel support for WebKit's sandbox. |
| Windows x64 | Windows 10/11 or a compatible Windows Server version. Desktop mode requires a graphical session; the release includes WebView2. |

Shelfmark needs permission to read the library and a writable data directory.
Saving metadata also requires write access to book files and their folders.
Desktop mode requires writable temporary storage. WSL requires WSLg or another
display server for a Linux window.

Release executables include offline Tesseract OCR with English and French
language models. Internet access is required for catalog searches and remote
cover downloads, but not for scanning or editing local books.

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
folders and update book files. Bind to loopback unless network clients are
trusted. Use an authenticated reverse proxy or an SSH tunnel for remote access.

Stop an HTTP-only server with Ctrl+C. Closing the desktop window stops Shelfmark.

### Data and logs

By default, Shelfmark creates `shelfmark.sqlite3` and `shelfmark.log` beside its
executable, regardless of the directory it was launched from. Keep the executable
in a writable folder. `-data-dir` changes the database location; the log file
stays beside the executable. Logs are appended across launches and may be removed
when Shelfmark is stopped.

Choose the minimum log level in **Settings → Logging**. The default is `info`;
`debug` adds diagnostic details:

```sh
./shelfmark-linux-amd64 -log-level debug
```

The log level is saved in the local database. `-log-level` overrides it for the
current launch. Logs may include local file paths; debug logs may include search
terms and candidate titles. API keys, request bodies, book text, and OCR output
are not logged.

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
The books mount needs write access to save metadata; mount it as `readonly` for
scanning only. The process uses UID/GID `10001:10001`. Grant that identity access
to bind-mounted books and, when `/data` is a bind mount, write access there. A
Docker named data volume is initialized with the image's ownership.

On Windows with Docker Desktop's Linux containers, use a Windows source path for
the books mount. Paths entered in Shelfmark still refer to the container, such as
`/books`, rather than a host drive letter. The image supports Linux/amd64; other
architectures need a corresponding build or Docker's platform emulation.

Additional flags can be appended after the image name, for example
`-log-level debug`. Keep the loopback port mapping unless remote access is intended;
the application does not provide authentication. Stop it with `docker stop shelfmark`.

## Settings

Settings are stored on the server. Changes affect future operations; scanning
preferences take effect on the next scan.

| Category | Controls |
| --- | --- |
| Library | Last folder, recursive scanning, hidden files and folders |
| Catalogs | Enable Google Books, Open Library, and Internet Archive individually; optional Google Books API key |
| Matching | Local content inspection, optional OCR, maximum candidates, cache duration, background search delay |
| Logging | Minimum severity (debug, info, warning, error), applied immediately when saved |

Catalog searches use search terms and discovered ISBNs. Book text is inspected
locally and is not uploaded. OCR searches opening images in EPUB and CBZ files;
PDF matching reads its text layer. Release executables include English and
French OCR models. Custom search terms bypass local content inspection.

Cached results appear when a book is selected. Use **Find matches** to search
catalogs, or **Ignore cache** to run a fresh search. Cache duration and size are
configurable. Catalog results can describe a different edition; Open Library
results may describe a work rather than a specific edition.

API keys are stored unencrypted in the local database and are not sent to the
browser. Leave the key field blank to retain a saved key, or remove it in
settings.

## Files and data

| Format | Metadata storage | Cover replacement |
| --- | --- | --- |
| EPUB | Package metadata, including additional Calibre properties | Supported |
| FB2 | Description metadata and additional Shelfmark properties | Not supported |
| CBZ | `ComicInfo.xml` | Not supported; archive pages remain intact |
| PDF | Document properties | Not supported |

Additional properties may not be displayed by every ebook reader. FB2 supports
one language field. DRM-protected, encrypted, or malformed books may not be
editable.

Selected updates replace the original file through a temporary file in the same
folder. File permissions are preserved. **Shelfmark does not create backup
copies.** Back up your collection before editing. Saves check for changes made
since review; books are saved independently, with failures reported per book.

`shelfmark.sqlite3` contains settings, catalog caches, and drafts. Stop Shelfmark
before copying its data directory. Book files are stored separately. Databases
are migrated automatically when opened; keep a copy if you need to use an older
version later.

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
