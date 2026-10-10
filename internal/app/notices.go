package app

import (
	"net/http"
	"shelfmark/internal/notices"
)

// licenseArchive provides attribution even when the release is a single binary.
func (s *Server) licenseArchive(w http.ResponseWriter, _ *http.Request) {
	archive := notices.Archive()
	if len(archive) == 0 {
		http.Error(w, "Notices are included in scripted release builds; see THIRD_PARTY.md in the source tree.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="shelfmark-licenses.tar.gz"`)
	_, _ = w.Write(archive)
}

func (s *Server) webviewLicense(w http.ResponseWriter, _ *http.Request) {
	terms, err := notices.Read("licenses/WebView2-Fixed-Version.html")
	if err != nil {
		http.Error(w, "WebView2 terms unavailable in this build", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(terms)
}
