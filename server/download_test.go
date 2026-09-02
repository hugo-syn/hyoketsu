package main

import (
	"database/sql"
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

// makeSQLiteFixture builds a small SQLite db mirroring the schema
// importFromSQLite reads. Tables listed in withRows get two rows each; tables
// listed in empty are created but left empty.
func makeSQLiteFixture(t *testing.T, path string, withRows, empty []string) {
	t.Helper()
	sdb, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()

	for _, table := range append(append([]string{}, withRows...), empty...) {
		if _, err := sdb.Exec(`CREATE TABLE ` + table + ` (
			dll_name TEXT, source TEXT, package_name TEXT, version TEXT, hash TEXT
		)`); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range withRows {
		for i := 0; i < 2; i++ {
			if _, err := sdb.Exec(
				`INSERT INTO `+table+` (dll_name, source, package_name, version, hash) VALUES (?, ?, ?, ?, ?)`,
				"foo.dll", "nuget", "TestPkg", "1.0.0", "deadbeef",
			); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestValidateSQLiteDBWithRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "good.db")
	makeSQLiteFixture(t, path, []string{"known_dlls"}, nil)

	if err := validateSQLiteDB(path); err != nil {
		t.Fatalf("validateSQLiteDB on a populated db: %v", err)
	}
}

func TestValidateSQLiteDBEmptyTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.db")
	makeSQLiteFixture(t, path, nil, []string{"known_dlls", "known_jars"})

	if err := validateSQLiteDB(path); err == nil {
		t.Fatal("expected error when known_dlls/known_jars exist but are empty")
	}
}

func TestValidateSQLiteDBNotADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "garbage.db")
	// Simulates a CDN error page served with HTTP 200.
	if err := os.WriteFile(path, []byte("not a database"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := validateSQLiteDB(path); err == nil {
		t.Fatal("expected error when the file is not a SQLite database")
	}
}
