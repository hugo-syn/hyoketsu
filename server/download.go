package main

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var (
	hyoketsuIndexURL = "https://wordlists-cdn.assetnote.io/hyoketsu/"
	hyoketsuDBURL    = "https://wordlists-cdn.assetnote.io/hyoketsu/hyoketsu.db"
)

// The index page is a small directory listing; the db itself is multi-GB, so
// they get very different timeouts. Both matter because auto-update runs
// unattended in a background goroutine with no watchdog.
var (
	indexHTTPClient    = &http.Client{Timeout: 30 * time.Second}
	downloadHTTPClient = &http.Client{Timeout: 30 * time.Minute}
)

func init() {
	if v := os.Getenv("HYOKETSU_INDEX_URL"); v != "" {
		hyoketsuIndexURL = v
	}
	if v := os.Getenv("HYOKETSU_DB_URL"); v != "" {
		hyoketsuDBURL = v
	}
}

func fetchRemoteDBDate() (string, error) {
	resp, err := indexHTTPClient.Get(hyoketsuIndexURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// nginx autoindex format: <a href="hyoketsu.db">hyoketsu.db</a>  DD-Mon-YYYY HH:MM  size
	re := regexp.MustCompile(`hyoketsu\.db</a>\s+(\d{2}-\w{3}-\d{4})`)
	matches := re.FindSubmatch(body)
	if matches == nil {
		return "", fmt.Errorf("hyoketsu.db not found in remote index")
	}

	t, err := time.Parse("02-Jan-2006", string(matches[1]))
	if err != nil {
		return string(matches[1]), nil
	}
	return t.Format("January 2, 2006"), nil
}

func downloadDatabase(dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	resp, err := downloadHTTPClient.Get(hyoketsuDBURL)
	if err != nil {
		return fmt.Errorf("download database: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %s", resp.Status)
	}

	tmpPath := dbPath + ".download"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("download interrupted: %w", err)
	}
	f.Close()

	if err := os.Rename(tmpPath, dbPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("move database into place: %w", err)
	}

	return nil
}

// validateSQLiteDB sanity-checks a downloaded database before it is allowed to
// replace live data. A CDN error page served with HTTP 200, or a truncated
// transfer, would otherwise be imported over the top of a wiped ClickHouse.
// The sqlite driver is registered process-wide by import.go's blank import.
func validateSQLiteDB(path string) error {
	sdb, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open downloaded database: %w", err)
	}
	defer sdb.Close()

	totalRows := 0
	for _, table := range []string{"known_dlls", "known_jars"} {
		// Tolerate a missing table the same way importFromSQLite does.
		var name string
		if err := sdb.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			continue
		}

		var count int
		if err := sdb.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&count); err != nil {
			return fmt.Errorf("count rows in %s: %w", table, err)
		}
		totalRows += count
	}

	if totalRows == 0 {
		return fmt.Errorf("downloaded database has no rows in known_dlls/known_jars — may be corrupt or incomplete")
	}
	return nil
}
