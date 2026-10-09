package app

import (
	"log/slog"
	"net/http"
	"time"
)

// Requests are traced without query strings, bodies, or headers: these can
// contain catalog credentials or book content. Record status and elapsed time
// after completion, and give failed requests visibility at warning/error level.
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

type loggedResponse struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *loggedResponse) WriteHeader(status int) {
	if !w.written {
		w.status, w.written = status, true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *loggedResponse) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

// Unwrap preserves support for http.ResponseController operations.
func (w *loggedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
