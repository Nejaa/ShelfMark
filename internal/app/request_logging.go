package app

import (
	"log/slog"
	"net/http"
	"time"
)

// withRequestLogging traces requests without query strings, bodies, or headers:
// these can contain catalog credentials or book content. It records status and
// elapsed time after completion, and gives failed requests warning/error level.
func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		slog.Debug("HTTP request started", "method", r.Method, "path", r.URL.Path)

		response := &loggedResponse{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(response, r)

		level := slog.LevelDebug
		if response.status >= 500 {
			level = slog.LevelError
		} else if response.status >= 400 {
			level = slog.LevelWarn
		}

		slog.Log(r.Context(), level, "HTTP request completed", "method", r.Method,
			"path", r.URL.Path, "status", response.status, "duration", time.Since(started))
	})
}

// loggedResponse captures the first status code written so request logging can
// report the final response even when handlers only call Write.
type loggedResponse struct {
	http.ResponseWriter
	status  int
	written bool
}

// WriteHeader records the first observed status code before delegating.
func (w *loggedResponse) WriteHeader(status int) {
	if !w.written {
		w.status, w.written = status, true
	}

	w.ResponseWriter.WriteHeader(status)
}

// Write preserves the implicit 200 behavior of net/http while still recording it.
func (w *loggedResponse) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(data)
}

// Unwrap preserves support for http.ResponseController operations.
func (w *loggedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
