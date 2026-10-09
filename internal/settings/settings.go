// Package settings defines persisted user preferences and their defaults.
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// Preferences are stored together so an update is atomic. Missing JSON fields
// inherit defaults, allowing future preferences to be added without a migration.
type Preferences struct {
	// Diagnostics are applied immediately and restored on the next launch.
	LogLevel slog.Level `json:"log_level"`

	LastFolder string `json:"last_folder"`

	// Library traversal and local content inspection.
	Recursive      bool `json:"recursive"`
	IncludeHidden  bool `json:"include_hidden"`
	InspectContent bool `json:"inspect_content"`
	OCR            bool `json:"ocr"`

	// Catalog enablement and optional credentials.
	GoogleBooks       bool   `json:"google_books"`
	OpenLibrary       bool   `json:"open_library"`
	InternetArchive   bool   `json:"internet_archive"`
	GoogleBooksAPIKey string `json:"google_books_api_key,omitempty"`

	// Search limits, cache lifetime, and browser background matching delay.
	MaxCandidates   int `json:"max_candidates"`
	CacheDays       int `json:"cache_days"`
	BackgroundDelay int `json:"background_delay_seconds"`
}

// Repository is the key/value persistence needed by user preferences.
type Repository interface {
	GetSetting(context.Context, string) (string, error)
	SetSetting(context.Context, string, string) error
}

// Defaults supplies values for both first launch and newly introduced fields.
func Defaults() Preferences {
	return Preferences{
		LogLevel:        slog.LevelInfo,
		Recursive:       true,
		InspectContent:  true,
		OCR:             true,
		GoogleBooks:     true,
		OpenLibrary:     true,
		InternetArchive: true,
		MaxCandidates:   12,
		CacheDays:       7,
		BackgroundDelay: 10,
	}
}

// Load overlays persisted values on defaults and accepts legacy settings.
func Load(ctx context.Context, db Repository) (Preferences, error) {
	p := Defaults()
	raw, err := db.GetSetting(ctx, "preferences")
	if err != nil {
		return p, err
	}

	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return p, fmt.Errorf("read preferences: %w", err)
		}
	} else {
		p.LastFolder, err = db.GetSetting(ctx, "last_folder")
		if err != nil {
			return p, err
		}

		p.GoogleBooksAPIKey, err = db.GetSetting(ctx, "google_books_api_key")
		if err != nil {
			return p, err
		}
	}

	return p, p.Validate()
}

// Validate checks bounds before settings can affect filesystem or network work.
func (p Preferences) Validate() error {
	switch p.LogLevel {
	case slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError:
	default:
		return fmt.Errorf("log level must be debug, info, warn, or error")
	}

	if len(p.LastFolder) > 4096 || strings.ContainsRune(p.LastFolder, 0) {
		return fmt.Errorf("invalid library folder")
	}

	if p.MaxCandidates < 1 || p.MaxCandidates > 50 {
		return fmt.Errorf("maximum candidates must be between 1 and 50")
	}

	if p.CacheDays < 0 || p.CacheDays > 365 {
		return fmt.Errorf("cache duration must be between 0 and 365 days")
	}

	if p.BackgroundDelay < 1 || p.BackgroundDelay > 300 {
		return fmt.Errorf("background delay must be between 1 and 300 seconds")
	}

	if len(p.GoogleBooksAPIKey) > 512 || strings.ContainsAny(p.GoogleBooksAPIKey, "\r\n") {
		return fmt.Errorf("invalid Google Books API key")
	}

	return nil
}

// Save validates and persists the complete preference snapshot.
func Save(ctx context.Context, db Repository, p Preferences) error {
	if err := p.Validate(); err != nil {
		return err
	}

	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}

	return db.SetSetting(ctx, "preferences", string(raw))
}
