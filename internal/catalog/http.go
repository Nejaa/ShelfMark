package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// throttle reserves one request slot at a time without holding the mutex while
// sleeping. Background searches yield while any manual search is waiting.
// Rechecking after each wait handles competing callers and cancellation.
func (s *Service) throttle(ctx context.Context) {
	background, _ := ctx.Value(priorityContextKey{}).(bool)
	for {
		s.mu.Lock()
		if background && s.manualWaiting > 0 {
			s.mu.Unlock()
			if !pause(ctx, 100*time.Millisecond) {
				return
			}
			continue
		}

		wait := time.Until(s.lastRequest.Add(1200 * time.Millisecond))
		if wait <= 0 {
			s.lastRequest = time.Now()
			s.mu.Unlock()
			return
		}

		s.mu.Unlock()
		if !pause(ctx, wait) {
			return
		}
	}
}

func pause(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type catalogHTTPError struct {
	StatusCode int
	Status     string
}

func (e *catalogHTTPError) Error() string { return "catalog returned " + e.Status }

func (s *Service) getJSON(ctx context.Context, raw string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "Shelfmark/1.0 (local ebook metadata helper)")
	slog.Debug("waiting for catalog request slot", "host", req.URL.Hostname())
	s.throttle(ctx)
	slog.Debug("sending catalog request", "host", req.URL.Hostname())
	resp, err := s.http.Do(req)
	if err != nil {
		slog.Debug("catalog transport failed", "host", req.URL.Hostname(), "error_type", fmt.Sprintf("%T", err), "reason", requestFailure(err))
		return err
	}

	defer func() { _ = resp.Body.Close() }()
	slog.Debug("catalog HTTP response", "host", req.URL.Hostname(), "status", resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		hint := "check catalog availability and try again later"
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			hint = "check the catalog API key and quota in Settings"
		} else if resp.StatusCode == http.StatusTooManyRequests {
			hint = "catalog rate limit reached; increase the background delay in Settings and retry later"
		}
		if resp.StatusCode == http.StatusNotFound {
			slog.Debug("catalog record not found", "host", req.URL.Hostname())
		} else {
			slog.Warn("catalog rejected request", "host", req.URL.Hostname(), "status", resp.StatusCode, "hint", hint)
		}
		return &catalogHTTPError{StatusCode: resp.StatusCode, Status: resp.Status}
	}

	err = json.NewDecoder(io.LimitReader(resp.Body, 5<<20)).Decode(dst)
	if err != nil {
		slog.Warn("catalog response could not be decoded", "host", req.URL.Hostname(), "error_type", fmt.Sprintf("%T", err))
	}
	return err
}

// requestFailure describes transport failures without exposing URLs, query
// strings, or credentials embedded in the errors returned by net/http.
func requestFailure(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "DNS resolution failed"
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout()) {
		return "network timeout"
	}
	var connection *net.OpError
	if errors.As(err, &connection) {
		return "network connection failed"
	}
	return "HTTP transport failed"
}
