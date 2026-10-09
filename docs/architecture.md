# Architecture

Shelfmark is a single process application for one collection owner. SQLite stores
local state; the filesystem remains the source of book contents. There is no
cluster coordination or distributed job system.

## Package responsibilities

| Package | Responsibility |
| --- | --- |
| `cmd/shelfmark` | Flags, executable-relative data and logging, console attachment, lifecycle, database and catalog wiring, HTTP listener |
| `cmd/shelfmark-window` | Native window process and SPA readiness bridge |
| `internal/ocrruntime` | Private OCR extraction, persistent language-model installation and cleanup |
| `internal/desktop` | Embedded runtime extraction, relocation, process lifecycle and failure isolation |
| `internal/app` | HTTP actions, library projections, background jobs, drafts, file and database coordination |
| `internal/library` | Book state, metadata schema, input parsing, search terms, ordering and draft differences |
| `internal/settings` | Preference defaults, validation, persistence compatibility |
| `internal/store` | SQLite connection, schema migrations, books, settings, bounded search cache |
| `internal/catalog` | Catalog adapters, request pacing, matching, ranking and metadata proposals |
| `internal/metadata` | Format readers and writers, covers, ISBN extraction, optional local OCR |
| `internal/webui` | Embedded SPA shell, styles and browser modules |

The application consumes persistence and catalog interfaces. The persistence
implementation does not depend on file parsers. Catalog searches receive explicit
options for each request rather than reading the database. Settings have one
validated model; a JSON document makes preference updates atomic and supplies
defaults for fields added by future versions.

## State and concurrency

Scanned book paths are held in memory. Starting a new scan replaces that index;
the library view API reads that index and persisted drafts. Folder membership and
filtering use server paths, so browser platform conventions cannot affect them.
Drafts are persisted by absolute path and can be reviewed and saved without an
active scan. IDs are derived from paths; moving a book does not move its draft.

Scans, draft edits, saves, and preference updates are serialized inside the
process. Read operations use a shared lock while accessing editable files. Two
searches may run concurrently; provider HTTP requests are paced and manual
searches take priority. Request cancellation reaches archive inspection, OCR,
HTTP lookups, and archive writing. Parser calls that do not expose cancellation
finish their current operation before stopping.

Drafts contain changed fields rather than copies of all metadata. A save receives
the reviewed file fingerprint and draft version, verifies both, writes selected
fields, and keeps any unselected changes. Schema version 2 converts older drafts
into this representation.

File replacement and SQLite are separate commits. After a successful write,
database bookkeeping completes even if the browser disconnects. If bookkeeping
fails, the draft remains available for review. Save batches report individual
failures; they cannot roll back already replaced files. Fingerprints detect size,
modification time and metadata changes, but they are not full content checksums
or locks against another program editing a file concurrently.

## Frontend

`index.html` contains the application shell, settings form and native dialogs.
`css/app.css` contains layout and component styles. Modules have explicit roles:

- `app.js`: renders library/folder projections, submits actions and displays job progress.
- `editor.js`: renders server-provided comparisons, binds raw form values and checkboxes.
- `review.js`: persistent draft review and selected saves.
- `settings.js`: preference loading, form updates and navigation.
- `common.js`: API requests, escaping, display formatting, labels and dialogs.

The Go backend owns application decisions: default search terms, natural series
ordering, folder membership, filtering, candidate mapping, metadata normalization,
field differences, supported operations and background scheduling. JavaScript
owns presentation and transient interaction state: forms, checkboxes, dialogs,
loading indicators, polling and protection against stale responses. It neither
splits metadata lists nor decides which fields differ or which formats can write
covers. Untouched candidate values retain their native types; only actual user
edits are parsed from raw text on the server.

`POST /api/library` returns the ordered, filtered books, folder tree and counts.
Scan, draft and save actions update server state; clients refresh this projection.
`POST /api/search` can omit the query to use backend defaults or supply explicit
user-entered terms. `POST /api/proposal` returns ordered editor fields and their
comparison/capability flags. `POST /api/apply` receives the chosen candidate, raw
edits and selected field names; Go constructs and validates the draft patch.

Background matching is one cancellable job per server. Start and cancel actions
use `/api/background/start` and `/api/background/cancel`; `GET /api/background`
reports status and progress. The worker reads settings, generates search terms,
respects manual lookup priority and controls delays. It survives browser closure,
but is canceled and joined on rescan or application shutdown. No job or scheduling
state is persisted and no distributed worker system is involved.

Editor state is provided through callbacks; the editor and review modules report
user actions to the library module. Responses for an old selection cannot update
the current book. UI content from files and catalogs is escaped before insertion.
The server rejects cross-origin API requests, limits JSON bodies, and sets a
content security policy that prevents inline scripts and framing.

## Catalog matching

`library.IdentifySearch` constructs an identity without changing book metadata.
It prioritizes specific subtitles when the title is just a series/volume, retains
original titles, and conservatively removes explicit filename noise. Separated
filenames supply an additional title clue without inventing an author.

`catalog.SearchRequest` separates this identity, ISBNs and explicit user terms
from provider options. A bounded plan searches at most eight variants per source:
ISBNs independently, title/author pairs, then title-only and plain-text fallbacks.
Each adapter translates the structured intent to its supported search syntax.
Failed sources stop for that request; exact ISBN hits stop further queries for
that source, and strong matches suppress broader fallbacks. Existing pacing,
manual priority and cancellation apply to every request.

Ranking combines bounded Unicode Levenshtein distance and token overlap, with
separate author, language and ISBN evidence. Initials and surname order are
handled independently of title tokens. Conflicting numbered titles and known
authors reduce scores. ISBNs must validate and ISBN-10/13 equivalents compare
equally. Records are deduplicated per source and URL before a global result limit.
Open Library work identifiers are retained as work records, with all returned
ISBNs available for matching and a note about edition ambiguity.

ISBN lookups also fetch Open Library edition JSON; the work record supplies
fallback author names, while the edition supplies its actual title, subtitle,
languages, series labels, publisher and date. Missing edition records are normal;
transport failures remain warnings and partial results are not cached. Explicit
contributor roles and labelled translation statements provide translator evidence.
EPUB readers recognize inline and refined contributor roles, and writes preserve
unselected roles.

`catalog.Reconcile` is shared by proposal rendering and draft application. It
separates numbered series headings from specific titles, promotes local subtitles,
retains localized titles and established edition fields, and uses filename and
ISBN-linked catalog clues as reviewable evidence. Each field has a reason and a
review flag; uncertain changes start unchecked. Manual edits override these
suggestions. No general web page scraping or inference from name shape is used.

Provider contracts: [Open Library search](https://openlibrary.org/dev/docs/api/search),
[Open Library editions and ISBNs](https://openlibrary.org/dev/docs/api/books),
[Google Books queries](https://developers.google.com/books/docs/v1/using),
[Internet Archive search](https://archivesupport.zendesk.com/hc/en-us/articles/360043648052-Search-Building-powerful-complex-queries).

A matching revision is included in cache keys so algorithm changes do not reuse
stale rankings. Explicit custom terms have a separate cache identity and do not
silently search the old metadata's ISBNs or title variants.

Browser failures are reported to `/api/ui-error` and logged with bounded payloads
and a server-wide rate limit. Search debug logs describe the identity, query plan,
provider result counts and individual ranking evidence.

## Extending the application

For a setting, add a typed field, default and validation in `settings`, a control
in the appropriate HTML section, and its effect in the operation that consumes
it. Omitted fields preserve existing values; secrets must be omitted from API
responses. Only expose settings that affect supported behavior. Listen addresses
and database locations remain startup flags.

For a catalog, implement a provider, register its display name and per-request
enablement in `catalog`, then expose the preference. Return provider errors so
they are distinguishable from searches with no results.

For a metadata field, register its value shape in `library`, add its frontend
label, its order in `library.FieldNames`, and implement the appropriate format mappings. Unsupported
fields should be explicitly rejected rather than silently discarded.

For a format, add read/write/cover/content handlers in `metadata` and register the
extension for scanning. Keep archive data streaming and original files intact
until replacement succeeds.

Long scans currently return one response. If collections grow beyond this model,
introduce a cancellable job with progress and paginated results inside the same
process. Persistent jobs are only needed if work must survive restarts.

## Desktop process boundary

The Go server has no native GUI linkage. A desktop build embeds a compressed
runtime archive; the parent extracts it only when a graphical session exists and
headless mode is not requested. It launches the helper with the server URL and
waits for a readiness message sent through stdout after the SPA initializes.
Missing libraries, a native crash, or a startup timeout return an error to the
parent, which continues serving HTTP. Closing a successfully loaded window exits
the helper normally and shuts down the server.

The helper watches stdin for EOF, so it exits if the parent disappears. Linux
helpers use a separate process group; cancellation and cleanup terminate their
subprocesses before removing extracted files. Runtime paths and environment
changes apply only to the child. See [Desktop runtime packaging](desktop.md).

## Startup and diagnostics

The executable directory supplies the default SQLite location and the persistent
log file. Startup parsing, logging, and platform console attachment are separate
from HTTP lifecycle management in `cmd/shelfmark`. Windows packaged executables
use the GUI subsystem and attach only to a console that already exists. Native
helper stderr joins the server log; helper stdout remains a readiness protocol.

Metadata entry points dispatch by format. Value normalization, ISBN recognition,
OCR, and text sampling live in focused files alongside the format readers and
writers. File replacement and draft reconciliation remain separate operations:
a completed file write cannot participate in a SQLite transaction.

Logging uses the standard `log/slog` text handler with a shared minimum level,
restored from preferences at startup unless `-log-level` overrides it. The app
receives the handler's `slog.LevelVar` through `WithLogLevel`, so saving a logging
preference updates the existing handler safely without rebuilding it. Normal events summarize application operations; debug
records trace requests, cache activity, providers, file processing, and OCR.
Native helper stderr is treated as a warning, while HTTP server diagnostics are
errors. Flag help and runtime crash reports bypass the severity filter. Catalog
transport errors are classified without logging credential-bearing URLs.

Optional OCR is checked when enabled. Tesseract discovery is cached on success;
failed language discovery and repeated availability warnings are limited to one
minute. Recognition failures remain visible without logging extracted text.

## OCR process boundary

Script-built desktop and headless executables embed a separate OCR archive. After
HTTP starts, `metadata.InitializeOCR` prepares the private engine and installs
English and French models into `tessdata/` beside the executable. Existing models
are preserved; copies are published only when complete. Cleanup runs after HTTP
shutdown, while language files remain for future launches.

Go decodes supported image formats and sends bounded grayscale PNM data over
stdin. The native helper links Tesseract, Leptonica and compiler runtimes without
native image-codec libraries. Recognition uses the LSTM engine, explicit model
paths, cancellation and a timeout. Failed native startup or recognition cannot
crash the server. Windows OCR processes use `CREATE_NO_WINDOW`.

Plain Go builds use an optional host Tesseract instead. Settings reports the
selected engine's availability and an actionable diagnostic, whether bundled or
installed on the server. Source versions, models and licenses are pinned in
`runtime/ocr`; Go build overlays embed the target-specific archive without
creating generated source-tree files.
