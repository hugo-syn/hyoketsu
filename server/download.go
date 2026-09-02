package main

import (
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
	hyoketsuDBURL     = "https://wordlists-cdn.assetnote.io/hyoketsu/hyoketsu.db"
)

func fetchRemoteDBDate() (string, error) {
	resp, err := http.Get(hyoketsuIndexURL)
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

	resp, err := http.Get(hyoketsuDBURL)
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
