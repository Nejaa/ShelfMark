package app

import (
	"context"
	"time"

	"shelfmark/internal/catalog"
	"shelfmark/internal/library"
	"shelfmark/internal/settings"
)

// Repository contains the persistence operations used by the application.
type Repository interface {
	settings.Repository
	CatalogCache
	BookRepository
}

// CatalogCache stores disposable search results independently of book drafts.
type CatalogCache interface {
	CacheGet(context.Context, string, time.Duration, any) (bool, error)
	CachePut(context.Context, string, any) error
}

// BookRepository persists file snapshots and patches of staged metadata.
type BookRepository interface {
	ReadBook(context.Context, string, string, map[string]any) (map[string]any, bool, error)
	Stage(context.Context, string, string, map[string]any, map[string]any) error
	State(context.Context, string) (library.State, error)
	Staged(context.Context) ([]library.State, error)
	DiscardDrafts(context.Context, []string) error
	FinishSave(context.Context, string, string, map[string]any, map[string]any) error
}

// CatalogSearcher supplies metadata candidates from external catalogs.
type CatalogSearcher interface {
	Search(context.Context, catalog.SearchRequest, catalog.Options) catalog.Result
}
