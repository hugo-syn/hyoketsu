package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchRemoteDBDate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="hyoketsu.db">hyoketsu.db</a>  15-Aug-2026  12G`))
	}))
	defer ts.Close()

	orig := hyoketsuIndexURL
	hyoketsuIndexURL = ts.URL + "/"
	defer func() { hyoketsuIndexURL = orig }()

	date, err := fetchRemoteDBDate()
	if err != nil {
		t.Fatal(err)
	}
	if date != "August 15, 2026" {
		t.Errorf("date = %q, want %q", date, "August 15, 2026")
	}
}

func TestFetchRemoteDBDateNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`nothing here`))
	}))
	defer ts.Close()

	orig := hyoketsuIndexURL
	hyoketsuIndexURL = ts.URL + "/"
	defer func() { hyoketsuIndexURL = orig }()

	if _, err := fetchRemoteDBDate(); err == nil {
		t.Fatal("expected error when hyoketsu.db not found in index")
	}
}

func TestDownloadDatabase(t *testing.T) {
	content := []byte("fake sqlite db content")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(content)
	}))
	defer ts.Close()

	orig := hyoketsuDBURL
	hyoketsuDBURL = ts.URL
	defer func() { hyoketsuDBURL = orig }()

	dir := t.TempDir()
	dst := filepath.Join(dir, "nested", "hyoketsu.db")

	if err := downloadDatabase(dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("downloaded content mismatch")
	}
	if _, err := os.Stat(dst + ".download"); !os.IsNotExist(err) {
		t.Errorf("expected temp file to be renamed away, got err=%v", err)
	}
}

func TestDownloadDatabaseHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	orig := hyoketsuDBURL
	hyoketsuDBURL = ts.URL
	defer func() { hyoketsuDBURL = orig }()

	dir := t.TempDir()
	dst := filepath.Join(dir, "hyoketsu.db")
	if err := downloadDatabase(dst); err == nil {
		t.Fatal("expected error on HTTP 404")
	}
}
