package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"shelfmark/internal/settings"
)

type matchProgress struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
	Failures  int    `json:"failures"`
}

// matchJob belongs to the server, not a browser tab. Its own lock lets lifecycle
// methods wait for completion without holding a lock needed by the worker.
type matchJob struct {
	mu       sync.Mutex
	progress matchProgress
	cancel   context.CancelFunc
	done     chan struct{}
}

func (j *matchJob) snapshot() matchProgress {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.progress
}

// Close cancels and joins background work before database or OCR cleanup.
func (s *Server) Close() { s.stopBackground() }

func (s *Server) stopBackground() {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	s.stopBackgroundLocked()
}

// stopBackgroundLocked joins without taking operations: a canceled worker may
// still need its read lock to finish. Callers hold jobsMu to prevent replacement.
func (s *Server) stopBackgroundLocked() {
	if s.background != nil {
		s.background.cancel()
		<-s.background.done
	}
}

func (s *Server) startBackground(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Folder string `json:"folder"`
	}
	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}
	// Serialize job replacement. The worker never takes jobsMu.
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	s.stopBackgroundLocked()
	view, err := s.scannedBooks(r.Context(), in.Folder, "")
	if err != nil {
		jsonError(w, err)
		return
	}
	s.jobSequence++
	ctx, cancel := context.WithCancel(s.lifetime)
	job := &matchJob{progress: matchProgress{ID: fmt.Sprint(s.jobSequence), Status: "running", Total: len(view.Books)}, cancel: cancel, done: make(chan struct{})}
	s.background = job
	slog.Info("background matching started", "job_id", job.progress.ID, "books", len(view.Books))
	go s.runBackground(ctx, job, view.Books)
	jsonOut(w, job.snapshot())
}

func (s *Server) cancelBackground(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		jsonError(w, err)
		return
	}
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if s.background != nil && s.background.snapshot().ID == in.ID {
		s.background.cancel()
		<-s.background.done
	}
	jsonOut(w, s.backgroundSnapshotLocked())
}

func (s *Server) backgroundSnapshotLocked() matchProgress {
	if s.background == nil {
		return matchProgress{Status: "idle"}
	}
	return s.background.snapshot()
}

func (s *Server) backgroundStatus(w http.ResponseWriter, _ *http.Request) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	jsonOut(w, s.backgroundSnapshotLocked())
}

func (s *Server) runBackground(ctx context.Context, job *matchJob, books []book) {
	defer close(job.done)
	defer job.cancel()
	defer func() {
		job.mu.Lock()
		if ctx.Err() != nil {
			job.progress.Status = "canceled"
		} else if job.progress.Status == "running" {
			job.progress.Status = "completed"
		}
		progress := job.progress
		job.mu.Unlock()
		slog.Info("background matching stopped", "job_id", progress.ID, "status", progress.Status, "completed", progress.Completed, "failures", progress.Failures)
	}()
	for i, b := range books {
		// Manual lookups own scheduling priority even during local OCR, before
		// requests reach the catalog service's separate network throttle.
		for s.manualSearches.Load() > 0 {
			if !waitBackground(ctx, 100*time.Millisecond) {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		result, err := s.findMatches(ctx, b.ID, nil, lookupOptions{Background: true})
		if ctx.Err() != nil {
			return
		}
		job.mu.Lock()
		job.progress.Completed++
		if err != nil || len(result.Warnings) > 0 {
			job.progress.Failures++
		}
		job.mu.Unlock()
		if err != nil {
			slog.Warn("background book lookup failed", "book_id", b.ID, "error", err)
		}
		if i+1 < len(books) {
			prefs, err := settings.Load(ctx, s.db)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Warn("background matching settings unavailable", "error", err)
				job.mu.Lock()
				job.progress.Status = "failed"
				job.progress.Failures++
				job.mu.Unlock()
				return
			}
			if !waitBackground(ctx, time.Duration(prefs.BackgroundDelay)*time.Second) {
				return
			}
		}
	}
}

func waitBackground(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
