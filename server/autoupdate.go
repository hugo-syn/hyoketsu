package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

func runAutoUpdate(ctx context.Context, store *CHStore, cachePath string, interval time.Duration) {
	const retryDelay = 5 * time.Minute
	lastImportedDate := ""
	for {
		date, err := fetchRemoteDBDate()
		if err != nil {
			log.Printf("auto-update: check for updates: %v", err)
			time.Sleep(retryDelay)
			continue
		}
		if date == lastImportedDate {
			log.Printf("auto-update: database unchanged (%s), skipping", date)
			time.Sleep(interval)
			continue
		}
		if err := autoUpdateOnce(ctx, store, cachePath); err != nil {
			log.Printf("auto-update: %v", err)
			time.Sleep(retryDelay)
			continue
		}
		lastImportedDate = date
		time.Sleep(interval)
	}
}

func autoUpdateOnce(ctx context.Context, store *CHStore, cachePath string) error {
	log.Printf("auto-update: downloading latest database...")
	if err := downloadDatabase(cachePath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	// Validate before truncating: a bad-but-HTTP-200 download must never be
	// allowed to wipe the live tables.
	log.Printf("auto-update: validating downloaded database...")
	if err := validateSQLiteDB(cachePath); err != nil {
		return fmt.Errorf("validate: %w", err)
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
