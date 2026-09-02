package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

func runAutoUpdate(ctx context.Context, store *CHStore, cachePath string, interval time.Duration) {
	for {
		if err := autoUpdateOnce(ctx, store, cachePath); err != nil {
			log.Printf("auto-update: %v", err)
		}
		time.Sleep(interval)
	}
}

func autoUpdateOnce(ctx context.Context, store *CHStore, cachePath string) error {
	log.Printf("auto-update: downloading latest database...")
	if err := downloadDatabase(cachePath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	log.Printf("auto-update: truncating existing tables...")
	if err := store.TruncateTables(ctx); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}

	log.Printf("auto-update: re-importing into clickhouse...")
	if err := importFromSQLite(ctx, store, cachePath); err != nil {
		return fmt.Errorf("import: %w", err)
	}

	log.Printf("auto-update: complete")
	return nil
}
