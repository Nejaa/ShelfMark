package settings

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

type memorySettings struct{ values map[string]string }

func (m *memorySettings) GetSetting(_ context.Context, key string) (string, error) {
	return m.values[key], nil
}

func (m *memorySettings) SetSetting(_ context.Context, key, value string) error {
	m.values[key] = value
	return nil
}

func TestDefaults(t *testing.T) {
	got := Defaults()
	if got.LogLevel != slog.LevelInfo || !got.Recursive || !got.InspectContent || !got.OCR || !got.GoogleBooks || !got.OpenLibrary || !got.InternetArchive {
		t.Fatalf("unexpected default preferences: %+v", got)
	}
	if got.MaxCandidates != 12 || got.CacheDays != 7 || got.BackgroundDelay != 10 {
		t.Fatalf("unexpected default limits: %+v", got)
	}
}

func TestPreferencesValidateBoundaries(t *testing.T) {
	valid := Defaults()
	valid.LastFolder = strings.Repeat("a", 4096)
	valid.GoogleBooksAPIKey = strings.Repeat("k", 512)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid boundary preferences rejected: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*Preferences)
	}{
		{"unknown log level", func(p *Preferences) { p.LogLevel = slog.Level(5) }},
		{"overlong folder", func(p *Preferences) { p.LastFolder += "x" }},
		{"NUL folder", func(p *Preferences) { p.LastFolder = "bad\x00path" }},
		{"zero candidate limit", func(p *Preferences) { p.MaxCandidates = 0 }},
		{"candidate limit too large", func(p *Preferences) { p.MaxCandidates = 51 }},
		{"negative cache age", func(p *Preferences) { p.CacheDays = -1 }},
		{"cache age too large", func(p *Preferences) { p.CacheDays = 366 }},
		{"zero background delay", func(p *Preferences) { p.BackgroundDelay = 0 }},
		{"background delay too large", func(p *Preferences) { p.BackgroundDelay = 301 }},
		{"API key too long", func(p *Preferences) { p.GoogleBooksAPIKey += "x" }},
		{"API key contains newline", func(p *Preferences) { p.GoogleBooksAPIKey = "bad\nkey" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			prefs := valid
			test.edit(&prefs)
			if err := prefs.Validate(); err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
		})
	}
}

func TestLoadDefaultsAndLegacySettings(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]string
		check  func(Preferences) error
	}{
		{name: "first launch", values: map[string]string{}, check: func(p Preferences) error {
			if p != Defaults() {
				return fmt.Errorf("got %+v, want defaults %+v", p, Defaults())
			}
			return nil
		}},
		{name: "legacy values", values: map[string]string{"last_folder": "/books", "google_books_api_key": "secret"}, check: func(p Preferences) error {
			if p.LastFolder != "/books" || p.GoogleBooksAPIKey != "secret" {
				return fmt.Errorf("legacy fields not loaded: %+v", p)
			}
			return nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Load(context.Background(), &memorySettings{values: test.values})
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if err := test.check(got); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLoadRejectsInvalidStoredPreferences(t *testing.T) {
	db := &memorySettings{values: map[string]string{"preferences": `{"max_candidates":999}`}}
	if _, err := Load(context.Background(), db); err == nil {
		t.Fatal("Load() accepted invalid stored settings")
	}
}

func TestSaveValidatesAndPersistsPreferences(t *testing.T) {
	db := &memorySettings{values: map[string]string{}}
	prefs := Defaults()
	prefs.LastFolder = "/books"
	prefs.GoogleBooksAPIKey = "secret"
	if err := Save(context.Background(), db, prefs); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load(context.Background(), db)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != prefs {
		t.Fatalf("loaded preferences = %+v, want %+v", got, prefs)
	}
	prefs.MaxCandidates = 0
	if err := Save(context.Background(), db, prefs); err == nil {
		t.Fatal("Save() accepted invalid preferences")
	}
}
