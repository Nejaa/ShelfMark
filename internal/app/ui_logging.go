package app

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// uiError records browser failures that cannot be seen by the HTTP handler.
// Reports are untrusted diagnostics: bound their size and rate, and resolve book
// paths through the scan index instead of accepting arbitrary client paths.
func (s *Server) uiError(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var in struct {
		Message string `json:"message"`
		Stack   string `json:"stack"`
		BookID  string `json:"book_id"`
	}
	if err := decode(r, &in); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	now := time.Now().UnixNano()
	last := s.lastUIError.Load()
	if strings.TrimSpace(in.Message) != "" && now-last >= int64(time.Second) && s.lastUIError.CompareAndSwap(last, now) {
		slog.Error("browser UI failed", "message", boundedDiagnostic(in.Message, 1000), "stack", boundedDiagnostic(in.Stack, 4000), "path", s.lookupPath(in.BookID))
	}
	w.WriteHeader(http.StatusNoContent)
}

func boundedDiagnostic(value string, limit int) string {
	runes := []rune(value)
	return string(runes[:min(len(runes), limit)])
}
