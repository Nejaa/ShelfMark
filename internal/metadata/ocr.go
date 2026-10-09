package metadata

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"shelfmark/internal/ocrruntime"
)

// Remember successful discovery without repeatedly spawning Tesseract for every
// image. Failures remain retryable, so installing it does not require a restart.
var ocrState struct {
	sync.Mutex
	path, language                    string
	runtime                           *ocrruntime.Runtime
	setupError                        error
	diagnostic                        string
	diagnosticMessage, diagnosticHint string
	failedPath                        string
	retryAfter                        time.Time
	lastWarning                       string
	warnedAt                          time.Time
}

// InitializeOCR prepares a packaged engine independently of the desktop helper.
// Call its cleanup only after HTTP requests and background operations stop.
// Direct Go builds retain support for an optional Tesseract installation.
func InitializeOCR(ctx context.Context, programDir string) (func(), error) {
	if !ocrruntime.Bundled() {
		return func() {}, nil
	}
	ocrState.Lock()
	defer ocrState.Unlock()
	engine, err := ocrruntime.Prepare(ctx, programDir)
	ocrState.runtime, ocrState.setupError = engine, err
	if err != nil {
		return func() {}, err
	}
	return engine.Close, nil
}

// OCRStatus describes capabilities on the server, including for remote clients.
type OCRStatus struct {
	Available bool   `json:"available"`
	Source    string `json:"source"`
	Warning   string `json:"warning,omitempty"`
}

// OCRAvailability checks the selected engine rather than just the server's PATH.
// Disabled OCR still gets an actionable Settings diagnostic without warning logs.
func OCRAvailability(ctx context.Context, enabled bool) OCRStatus {
	_, _, available := ocrConfiguration(ctx, enabled)
	ocrState.Lock()
	defer ocrState.Unlock()
	source := "system"
	if ocrruntime.Bundled() {
		source = "bundled"
	}
	return OCRStatus{Available: available, Source: source, Warning: ocrState.diagnostic}
}

// CheckOCR reports availability and logs actionable warnings for missing tools
// or language data. Call only when local OCR is enabled by the user.
func CheckOCR(ctx context.Context) bool {
	_, _, ok := ocrConfiguration(ctx, true)
	return ok
}

func ocrConfiguration(parent context.Context, report bool) (string, string, bool) {
	ocrState.Lock()
	defer ocrState.Unlock()

	if parent.Err() != nil {
		return "", "", false
	}
	if ocrruntime.Bundled() && ocrState.runtime == nil && ocrState.setupError == nil {
		ocrState.diagnostic = "Bundled OCR is still being prepared. Reopen Settings shortly."
		return "", "", false
	}
	if ocrState.setupError != nil {
		ocrWarning(report, "OCR unavailable: bundled engine could not be prepared", "check shelfmark.log; the executable folder and temporary directory must be writable, and the temporary directory must allow executable files")
		return "", "", false
	}
	path := ""
	if ocrState.runtime != nil {
		path = ocrState.runtime.Executable
	} else {
		var err error
		path, err = exec.LookPath("tesseract")
		if err != nil {
			ocrWarning(report, "OCR unavailable: Tesseract was not found", "install Tesseract OCR with English or French language data and add it to PATH; or disable OCR in Settings")
			return "", "", false
		}
	}
	if path == ocrState.path && ocrState.language != "" {
		return path, ocrState.language, true
	}

	if path == ocrState.failedPath && time.Now().Before(ocrState.retryAfter) {
		ocrWarning(report, ocrState.diagnosticMessage, ocrState.diagnosticHint)
		return "", "", false
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	languages := exec.CommandContext(ctx, path, ocrArgumentsLocked("--list-langs")...)
	configureCommand(languages)
	out, err := languages.Output()
	if err != nil {
		if parent.Err() == nil {
			ocrState.failedPath, ocrState.retryAfter = path, time.Now().Add(time.Minute)
			ocrWarning(report, "OCR unavailable: Tesseract language discovery failed", ocrHint("check that tesseract --list-langs works and TESSDATA_PREFIX points to installed language data"))
		}
		slog.Debug("Tesseract language discovery failed", "error", err)
		return "", "", false
	}

	hasEnglish, hasFrench := false, false
	for _, language := range strings.Fields(string(out)) {
		hasEnglish = hasEnglish || language == "eng"
		hasFrench = hasFrench || language == "fra"
	}
	language := ""
	switch {
	case hasEnglish && hasFrench:
		language = "fra+eng"
	case hasEnglish:
		language = "eng"
	case hasFrench:
		language = "fra"
	default:
		ocrState.failedPath, ocrState.retryAfter = path, time.Now().Add(time.Minute)
		ocrWarning(report, "OCR unavailable: English and French language data are missing", ocrHint("install Tesseract eng or fra language data; check TESSDATA_PREFIX, or disable OCR in Settings"))
		return "", "", false
	}
	ocrState.path, ocrState.language = path, language
	ocrState.lastWarning, ocrState.failedPath, ocrState.diagnostic = "", "", ""
	slog.Info("OCR available", "engine", path, "languages", language)
	return path, language, true
}

// ocrWarning is called with ocrState locked. Rate limiting prevents one missing
// dependency from generating the same warning for every image in a collection.
func ocrWarning(report bool, message, hint string) {
	ocrState.diagnostic = message + ". " + hint + "."
	ocrState.diagnosticMessage, ocrState.diagnosticHint = message, hint
	if report && (message != ocrState.lastWarning || time.Since(ocrState.warnedAt) >= time.Minute) {
		slog.Warn(message, "hint", hint)
		ocrState.lastWarning, ocrState.warnedAt = message, time.Now()
	}
}

// ocrArgumentsLocked is called with ocrState locked.
// The bundled engine always uses our persistent model directory, regardless of
// a host TESSDATA_PREFIX value.
func ocrArgumentsLocked(args ...string) []string {
	if ocrState.runtime != nil {
		args = append(args, "--tessdata-dir", ocrState.runtime.DataDir)
	}
	return args
}

// ocrHint is called with ocrState locked.
func ocrHint(systemHint string) string {
	if ocrState.runtime != nil {
		return "check shelfmark.log and temporary-directory execution permissions; remove damaged eng.traineddata or fra.traineddata files from tessdata beside the executable and restart to restore them"
	}
	return systemHint
}

// OCR invokes Tesseract with a timeout inherited from the caller.
// Failures produce no clues; diagnostics never contain recognized book text.
func OCR(parent context.Context, data []byte) string {
	if len(data) == 0 || len(data) > 8<<20 {
		slog.Debug("OCR image skipped", "bytes", len(data), "reason", "empty image or size limit exceeded")
		return ""
	}
	path, language, ok := ocrConfiguration(parent, true)
	if !ok {
		return ""
	}

	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	started := time.Now()
	pixels, err := ocrImage(ctx, data)
	if err != nil {
		slog.Debug("OCR image skipped", "error", err)
		return ""
	}
	slog.Debug("OCR image recognition started", "bytes", len(data), "languages", language)
	ocrState.Lock()
	args := ocrArgumentsLocked("stdin", "stdout", "--oem", "1", "--psm", "6", "-l", language)
	ocrState.Unlock()
	cmd := exec.CommandContext(ctx, path, args...)
	configureCommand(cmd)
	cmd.Stdin = bytes.NewReader(pixels)
	out, err := cmd.Output()
	if err != nil {
		switch {
		case parent.Err() != nil:
			slog.Debug("OCR canceled", "error", parent.Err())
		case ctx.Err() != nil:
			slog.Warn("OCR timed out; image skipped", "timeout", "25s", "hint", "disable OCR in Settings if image recognition is too slow")
		default:
			slog.Warn("OCR image recognition failed; image skipped", "error", err, "hint", "check Tesseract and its language data; use -log-level debug for operation details")
		}
		return ""
	}

	slog.Debug("OCR image recognition completed", "duration", time.Since(started), "output_bytes", len(out))
	return strings.TrimSpace(string(out))
}
