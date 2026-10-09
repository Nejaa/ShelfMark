package main

import (
	"context"

	"shelfmark/internal/desktop"
)

func openDesktop(ctx context.Context, url string) error {
	return desktop.Run(ctx, url)
}
