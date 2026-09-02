# Remote Extract + Server Auto-Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `--remote` flag to `hyoketsu extract` (mirroring `scan --remote`) so extraction of unknown files can be driven by the ClickHouse-backed server instead of a local db, and wire the server + docker-compose so the server auto-downloads the latest prebuilt db and re-imports it into ClickHouse on a schedule, with no manual `-import-sqlite` step required.

**Architecture:** Two independent subsystems sharing one branch/PR. (1) CLI: `cmd/scan.go`'s remote-lookup logic is factored into a reusable `remoteScan` helper; `cmd/extract.go` gains a `--remote` flag that calls it, and its file-copy loop is factored into a reusable `extractResults` helper so both the local-db and remote-server code paths feed the same extraction logic. (2) Server: a new `server/download.go` duplicates the CDN download logic from `cmd/download.go` (the two are separate Go modules and can't share code directly), a `TruncateTables` method is added to `CHStore`, and a background ticker in `server/autoupdate.go` downloads + truncates + re-imports on an interval. `docker-compose.yml` gains a `server` service (built from a new `server/Dockerfile`) wired to ClickHouse with `-auto-update` enabled by default.

**Tech Stack:** Go 1.24 (two modules: root `hyoketsu`, and `hyoketsu/server`), Cobra CLI, ClickHouse (`clickhouse-go/v2`), `modernc.org/sqlite`, `net/http/httptest` for tests, Docker/Podman Compose.

**Spec:** This plan's Architecture section above (no separate spec doc — requirements came directly from the user's request and the design notes captured in conversation).

## Global Constraints

- Root module and `server/` module are separate Go modules (`hyoketsu` vs `hyoketsu/server`) — they cannot import each other's packages. Duplication of the small download helper is intentional, not an oversight.
- Follow existing code style: no doc comments beyond what already exists in each file, package-level vars for CLI flag storage (matches `remoteURL`, `extractFlat`, etc.), `fmt.Errorf("...: %w", err)` wrapping.
- No CGO: `modernc.org/sqlite` and `clickhouse-go/v2` are pure Go, so `CGO_ENABLED=0` must keep working (needed for the Alpine-based Dockerfile).
- Existing CLI flag `remoteURL` (declared in `cmd/scan.go`) is reused by `extract.go` rather than duplicated — same conceptual setting, same package.
- Existing behavior of `-import-sqlite` / `-import-nuget` (one-shot, then `os.Exit(0)`) must not change.
- ClickHouse-touching code (`clickhouse.go`, `import.go`) has no existing unit tests in this repo (no CH instance in CI) — follow that precedent; only add tests for logic that's testable without a live ClickHouse/network dependency (using `httptest`).

---

### Task 0: Create the feature branch

**Files:** none

- [ ] **Step 1: Create and switch to the branch**

```bash
git checkout -b feat/remote-extract-and-server-autoupdate
```

- [ ] **Step 2: Verify**

```bash
git branch --show-current
```
Expected: `feat/remote-extract-and-server-autoupdate`

---

### Task 1: Extract reusable `remoteScan` helper in `cmd/scan.go`

**Files:**
- Modify: `cmd/scan.go` (lines 113–164, the `scanRemote` function)
- Test: `cmd/scan_test.go` (new)

**Interfaces:**
- Produces: `remoteScan(target string) ([]scanner.Result, error)` — runs the same collect/hash/lookup/dedup-mark logic `scanRemote` currently does, but returns results instead of displaying them. Used by Task 2's `extractRemote`.
- Consumes: package-level `remoteURL string` (already declared in `cmd/scan.go`), `remoteLookup(files []remoteLookupFile) (*remoteLookupResponse, error)` (already declared), `scanner.CollectFiles`, `scanner.HashFile`, `scanner.Result`.

- [ ] **Step 1: Write the failing test**

Create `cmd/scan_test.go`:

```go
package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type mockLookupResult struct {
	Filename    string `json:"filename"`
	Status      string `json:"status"`
	MatchedBy   string `json:"matched_by"`
	Source      string `json:"source"`
	PackageName string `json:"package_name"`
}

type mockLookupResponse struct {
	Results []mockLookupResult `json:"results"`
}

func TestRemoteScan(t *testing.T) {
	dir := t.TempDir()
	dllPath := filepath.Join(dir, "foo.dll")
	if err := os.WriteFile(dllPath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	var gotReq remoteLookupRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lookup" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatal(err)
		}
		resp := mockLookupResponse{}
		for _, f := range gotReq.Files {
			resp.Results = append(resp.Results, mockLookupResult{
				Filename:    f.Filename,
				Status:      "Known",
				MatchedBy:   "hash",
				Source:      "nuget",
				PackageName: "TestPkg",
			})
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	origRemoteURL := remoteURL
	remoteURL = ts.URL
	defer func() { remoteURL = origRemoteURL }()

	results, err := remoteScan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Status != "Known" || results[0].Source != "nuget" || results[0].PackageName != "TestPkg" {
		t.Errorf("unexpected result: %+v", results[0])
	}
	if len(gotReq.Files) != 1 || gotReq.Files[0].Filename != "foo.dll" {
		t.Errorf("unexpected request: %+v", gotReq.Files)
	}
}

func TestRemoteScanMarksDuplicates(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.dll"), []byte("same"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.dll"), []byte("same"), 0644); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req remoteLookupRequest
		json.NewDecoder(r.Body).Decode(&req)
		resp := mockLookupResponse{}
		for _, f := range req.Files {
			resp.Results = append(resp.Results, mockLookupResult{Filename: f.Filename, Status: "Unknown"})
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	origRemoteURL := remoteURL
	remoteURL = ts.URL
	defer func() { remoteURL = origRemoteURL }()

	results, err := remoteScan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	dupCount := 0
	for _, r := range results {
		if r.Duplicate {
			dupCount++
		}
	}
	if dupCount != 1 {
		t.Errorf("dupCount = %d, want 1 (one of the two identical files should be marked duplicate)", dupCount)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/... -run TestRemoteScan -v`
Expected: FAIL with `undefined: remoteScan`

- [ ] **Step 3: Implement `remoteScan` by refactoring `scanRemote`**

In `cmd/scan.go`, replace the existing `scanRemote` function (lines 113–164) with:

```go
func remoteScan(target string) ([]scanner.Result, error) {
	files, err := scanner.CollectFiles(target)
	if err != nil {
		return nil, err
	}

	req := make([]remoteLookupFile, len(files))
	results := make([]scanner.Result, len(files))
	for i := range files {
		scanner.HashFile(&files[i])
		req[i] = remoteLookupFile{
			Filename: strings.ToLower(files[i].Filename),
			Hash:     files[i].Hash,
			Type:     files[i].Type,
		}
		results[i] = scanner.Result{
			Filename: files[i].Filename,
			Path:     files[i].Path,
			Type:     files[i].Type,
			IsDotNet: files[i].IsDotNet,
			Hash:     files[i].Hash,
			Status:   "Unknown",
		}
	}

	resp, err := remoteLookup(req)
	if err != nil {
		return nil, err
	}
	for i := range results {
		if i < len(resp.Results) {
			sr := resp.Results[i]
			results[i].Status = sr.Status
			results[i].MatchedBy = sr.MatchedBy
			results[i].Source = sr.Source
			results[i].PackageName = sr.PackageName
		}
	}

	seenHashes := make(map[string]bool)
	for i := range results {
		if results[i].Hash != "" {
			if seenHashes[results[i].Hash] {
				results[i].Duplicate = true
			} else {
				seenHashes[results[i].Hash] = true
			}
		}
	}

	return results, nil
}

func scanRemote(target string) error {
	results, err := remoteScan(target)
	if err != nil {
		return err
	}
	return displayResults(results)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/... -run TestRemoteScan -v`
Expected: PASS

- [ ] **Step 5: Run the full existing cmd test suite to check for regressions**

Run: `go test ./cmd/...`
Expected: PASS (all existing tests in `cmd_test.go` still pass)

- [ ] **Step 6: Commit**

```bash
git add cmd/scan.go cmd/scan_test.go
git commit -m "refactor(scan): extract remoteScan helper for reuse by extract --remote"
```

---

### Task 2: Add `--remote` flag to `hyoketsu extract`

**Files:**
- Modify: `cmd/extract.go`
- Test: `cmd/extract_test.go` (new)

**Interfaces:**
- Consumes: `remoteScan(target string) ([]scanner.Result, error)` (Task 1), package-level `remoteURL string` (from `cmd/scan.go`), `scanner.Result`, `scanner.Scan`, `db.Open`.
- Produces: `extractResults(results []scanner.Result, source, dest string) (extracted, skippedKnown, skippedDupe int, err error)` — pure file-copy logic, independent of how `results` were obtained.

- [ ] **Step 1: Write the failing tests**

Create `cmd/extract_test.go`:

```go
package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"hyoketsu/scanner"
)

func TestExtractResultsSkipsKnownAndPreservesStructure(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	destDir := filepath.Join(dir, "dest")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	unknownPath := filepath.Join(srcDir, "unknown.dll")
	knownPath := filepath.Join(srcDir, "known.dll")
	nestedPath := filepath.Join(srcDir, "sub", "nested.dll")
	os.WriteFile(unknownPath, []byte("a"), 0644)
	os.WriteFile(knownPath, []byte("b"), 0644)
	os.WriteFile(nestedPath, []byte("c"), 0644)

	results := []scanner.Result{
		{Filename: "unknown.dll", Path: unknownPath, Status: "Unknown"},
		{Filename: "known.dll", Path: knownPath, Status: "Known"},
		{Filename: "nested.dll", Path: nestedPath, Status: "Unknown"},
	}

	extracted, skippedKnown, skippedDupe, err := extractResults(results, srcDir, destDir)
	if err != nil {
		t.Fatal(err)
	}
	if extracted != 2 {
		t.Errorf("extracted = %d, want 2", extracted)
	}
	if skippedKnown != 1 {
		t.Errorf("skippedKnown = %d, want 1", skippedKnown)
	}
	if skippedDupe != 0 {
		t.Errorf("skippedDupe = %d, want 0", skippedDupe)
	}
	if _, err := os.Stat(filepath.Join(destDir, "unknown.dll")); err != nil {
		t.Errorf("expected unknown.dll copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "sub", "nested.dll")); err != nil {
		t.Errorf("expected nested structure preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "known.dll")); err == nil {
		t.Errorf("known.dll should not have been extracted")
	}
}

func TestExtractResultsFlat(t *testing.T) {
	orig := extractFlat
	extractFlat = true
	defer func() { extractFlat = orig }()

	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	subDir := filepath.Join(srcDir, "sub")
	os.MkdirAll(subDir, 0755)
	destDir := filepath.Join(dir, "dest")
	p := filepath.Join(subDir, "x.dll")
	os.WriteFile(p, []byte("x"), 0644)

	results := []scanner.Result{{Filename: "x.dll", Path: p, Status: "Unknown"}}
	if _, _, _, err := extractResults(results, srcDir, destDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "x.dll")); err != nil {
		t.Errorf("expected flat copy directly into dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "sub", "x.dll")); err == nil {
		t.Errorf("flat mode should not preserve subdirectory structure")
	}
}

func TestExtractResultsDotnetOnly(t *testing.T) {
	orig := extractDotnetOnly
	extractDotnetOnly = true
	defer func() { extractDotnetOnly = orig }()

	dir := t.TempDir()
	destDir := filepath.Join(dir, "dest")
	native := filepath.Join(dir, "native.dll")
	dotnet := filepath.Join(dir, "dotnet.dll")
	os.WriteFile(native, []byte("n"), 0644)
	os.WriteFile(dotnet, []byte("d"), 0644)

	results := []scanner.Result{
		{Filename: "native.dll", Path: native, Status: "Unknown", IsDotNet: false},
		{Filename: "dotnet.dll", Path: dotnet, Status: "Unknown", IsDotNet: true},
	}
	extracted, _, _, err := extractResults(results, dir, destDir)
	if err != nil {
		t.Fatal(err)
	}
	if extracted != 1 {
		t.Errorf("extracted = %d, want 1", extracted)
	}
	if _, err := os.Stat(filepath.Join(destDir, "dotnet.dll")); err != nil {
		t.Errorf("expected dotnet.dll copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "native.dll")); err == nil {
		t.Errorf("native.dll should have been skipped under --dotnet-only")
	}
}

func TestExtractResultsDedup(t *testing.T) {
	orig := extractDedup
	extractDedup = true
	defer func() { extractDedup = orig }()

	dir := t.TempDir()
	destDir := filepath.Join(dir, "dest")
	a := filepath.Join(dir, "a.dll")
	b := filepath.Join(dir, "b.dll")
	os.WriteFile(a, []byte("a"), 0644)
	os.WriteFile(b, []byte("b"), 0644)

	results := []scanner.Result{
		{Filename: "a.dll", Path: a, Status: "Unknown", Duplicate: false},
		{Filename: "b.dll", Path: b, Status: "Unknown", Duplicate: true},
	}
	extracted, _, skippedDupe, err := extractResults(results, dir, destDir)
	if err != nil {
		t.Fatal(err)
	}
	if extracted != 1 || skippedDupe != 1 {
		t.Errorf("extracted=%d skippedDupe=%d, want 1,1", extracted, skippedDupe)
	}
}

func TestExtractRemoteFlagExists(t *testing.T) {
	f := extractCmd.Flags().Lookup("remote")
	if f == nil {
		t.Fatal("extract command missing --remote flag")
	}
	if f.DefValue != "" {
		t.Errorf("--remote default = %q, want empty string", f.DefValue)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/... -run TestExtractResults -v`
Expected: FAIL with `undefined: extractResults` (and `TestExtractRemoteFlagExists` fails because the flag doesn't exist yet)

- [ ] **Step 3: Implement `extractResults` and wire the `--remote` flag**

Replace `cmd/extract.go` in full with:

```go
package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"hyoketsu/db"
	"hyoketsu/scanner"

	"github.com/spf13/cobra"
)

var (
	extractDotnetOnly bool
	extractDedup      bool
	extractFlat       bool
)

var extractCmd = &cobra.Command{
	Use:   "extract <source> <dest>",
	Short: "Copy unknown files to a separate directory for decompilation",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		source := cleanPath(args[0])
		dest := cleanPath(args[1])

		var results []scanner.Result
		if remoteURL != "" {
			r, err := remoteScan(source)
			if err != nil {
				return err
			}
			results = r
		} else {
			store, err := db.Open(getDBPath())
			if err != nil {
				return err
			}
			defer store.Close()

			r, err := scanner.Scan(store, source)
			if err != nil {
				return err
			}
			results = r
		}

		extracted, skippedKnown, skippedDupe, err := extractResults(results, source, dest)
		if err != nil {
			return err
		}

		fmt.Printf("%d files extracted, %d skipped (known), %d skipped (duplicate)\n",
			extracted, skippedKnown, skippedDupe)
		return nil
	},
}

func extractResults(results []scanner.Result, source, dest string) (extracted, skippedKnown, skippedDupe int, err error) {
	for _, r := range results {
		if r.Status == "Known" {
			skippedKnown++
			continue
		}
		if extractDotnetOnly && !r.IsDotNet {
			continue
		}
		if extractDedup && r.Duplicate {
			skippedDupe++
			continue
		}

		var destPath string
		if extractFlat {
			destPath = filepath.Join(dest, r.Filename)
		} else {
			// Preserve subdirectory structure relative to source
			rel, relErr := filepath.Rel(source, r.Path)
			if relErr != nil {
				rel = r.Filename
			}
			destPath = filepath.Join(dest, rel)
		}

		if mkErr := os.MkdirAll(filepath.Dir(destPath), 0755); mkErr != nil {
			return extracted, skippedKnown, skippedDupe, fmt.Errorf("create directory: %w", mkErr)
		}

		if cpErr := copyFile(r.Path, destPath); cpErr != nil {
			return extracted, skippedKnown, skippedDupe, fmt.Errorf("copy %s: %w", r.Filename, cpErr)
		}
		extracted++
	}
	return extracted, skippedKnown, skippedDupe, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func init() {
	extractCmd.Flags().BoolVar(&extractDotnetOnly, "dotnet-only", false, "Only extract .NET assemblies (skip native DLLs)")
	extractCmd.Flags().BoolVar(&extractDedup, "dedup", false, "Skip duplicate files (by SHA256 hash)")
	extractCmd.Flags().BoolVar(&extractFlat, "flat", false, "Flatten into single directory (default: preserve subdirectory structure)")
	extractCmd.Flags().StringVar(&remoteURL, "remote", "", "Remote server URL (e.g. http://host:8080)")
}
```

Note: `remoteURL` is declared once in `cmd/scan.go` and bound to two separate flags (on `scanCmd` and `extractCmd`) — this is safe because Cobra flag sets are per-command and only one command's `RunE` executes per process invocation.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/... -run 'TestExtractResults|TestExtractRemoteFlagExists' -v`
Expected: PASS

- [ ] **Step 5: Run the full cmd test suite**

Run: `go test ./cmd/...`
Expected: PASS

- [ ] **Step 6: Manual smoke test against a real remote server (optional but recommended)**

If a server is reachable, run:
```bash
go build -o /tmp/hyoketsu .
/tmp/hyoketsu extract --remote http://localhost:8080 /path/to/binaries /tmp/extracted
```
Confirm output matches the local-db extract's format and only unmatched files land in `/tmp/extracted`.

- [ ] **Step 7: Commit**

```bash
git add cmd/extract.go cmd/extract_test.go
git commit -m "feat(extract): add --remote flag mirroring scan --remote"
```

---

### Task 3: Server-side DB download helper

**Files:**
- Create: `server/download.go`
- Test: `server/download_test.go`

**Interfaces:**
- Produces: `fetchRemoteDBDate() (string, error)`, `downloadDatabase(dbPath string) error`, package vars `hyoketsuIndexURL`, `hyoketsuDBURL` (mutable `var`, not `const`, so tests can point them at an `httptest.Server`). Used by Task 4's `autoUpdateOnce`.

- [ ] **Step 1: Write the failing tests**

Create `server/download_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./... -run 'TestFetchRemoteDBDate|TestDownloadDatabase' -v`
Expected: FAIL with `undefined: hyoketsuIndexURL` / `undefined: fetchRemoteDBDate` etc.

- [ ] **Step 3: Implement `server/download.go`**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && go test ./... -run 'TestFetchRemoteDBDate|TestDownloadDatabase' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/download.go server/download_test.go
git commit -m "feat(server): add DB download helper for auto-update"
```

---

### Task 4: ClickHouse table truncation + background auto-update loop

**Files:**
- Modify: `server/clickhouse.go` (add `TruncateTables` method)
- Create: `server/autoupdate.go`
- Modify: `server/main.go` (add flags, wire the background loop)

**Interfaces:**
- Consumes: `downloadDatabase(dbPath string) error` (Task 3), `importFromSQLite(ctx, ch, sqlitePath) error` (existing, `server/import.go`).
- Produces: `(*CHStore).TruncateTables(ctx context.Context) error`; `runAutoUpdate(ctx, store, cachePath string, interval time.Duration)`; `autoUpdateOnce(ctx, store, cachePath string) error`.

- [ ] **Step 1: Add `TruncateTables` to `server/clickhouse.go`**

Insert immediately after the `CreateSchema` method (after line 64, before `func chTableForType`):

```go
func (s *CHStore) TruncateTables(ctx context.Context) error {
	for _, table := range []string{"known_dlls", "known_jars"} {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`TRUNCATE TABLE %s`, table)); err != nil {
			return fmt.Errorf("truncate %s: %w", table, err)
		}
	}
	return nil
}
```

This is ClickHouse-touching code with no live instance available for unit tests (consistent with the rest of `clickhouse.go`) — verified manually in Task 6's docker-compose smoke test instead.

- [ ] **Step 2: Create `server/autoupdate.go`**

```go
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
```

- [ ] **Step 3: Wire flags and the background goroutine into `server/main.go`**

Add `"time"` to the import block (after `"os"`):

```go
import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)
```

Replace the flag declarations (lines 27–31) with:

```go
	listenAddr := flag.String("listen", ":8080", "HTTP listen address")
	chDSN := flag.String("clickhouse", "clickhouse://localhost:9000/default", "ClickHouse DSN")
	importSQLite := flag.String("import-sqlite", "", "Import data from SQLite DB path, then exit")
	importNuGet := flag.String("import-nuget", "", "Import NuGet JSONL data from directory (containing crawl/ and hashes/), then exit")
	autoUpdate := flag.Bool("auto-update", false, "periodically download the latest prebuilt db and re-import it into clickhouse")
	autoUpdateInterval := flag.Duration("auto-update-interval", 30*24*time.Hour, "interval between automatic db re-imports (used with -auto-update)")
	dbCache := flag.String("db-cache", "/data/hyoketsu.db", "local path to cache the downloaded db before importing (used with -auto-update)")
	flag.Parse()
```

Insert this block right after the `*importNuGet` branch (after the closing `}` that follows its `os.Exit(0)`, i.e. after current line 58) and before the `http.HandleFunc("POST /lookup", ...)` registration:

```go
	if *autoUpdate {
		go runAutoUpdate(ctx, store, *dbCache, *autoUpdateInterval)
	}
```

- [ ] **Step 4: Build to catch compile errors**

Run: `cd server && go build ./...`
Expected: builds cleanly (no unused imports/vars)

- [ ] **Step 5: Run the full server test suite**

Run: `cd server && go test ./...`
Expected: PASS (Task 3's tests still pass; no new test failures)

- [ ] **Step 6: Commit**

```bash
git add server/clickhouse.go server/autoupdate.go server/main.go
git commit -m "feat(server): add -auto-update flag with periodic DB re-import"
```

---

### Task 5: Dockerfile + docker-compose wiring

**Files:**
- Create: `server/Dockerfile`
- Modify: `docker-compose.yml`

**Interfaces:**
- Consumes: `server` module build (`go build .` inside `server/`), the `-auto-update`, `-auto-update-interval`, `-db-cache`, `-clickhouse` flags from Task 4.

- [ ] **Step 1: Create `server/Dockerfile`**

```dockerfile
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/hyoketsu-server .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/hyoketsu-server /usr/local/bin/hyoketsu-server
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/hyoketsu-server"]
```

- [ ] **Step 2: Update `docker-compose.yml`**

Replace the full file with:

```yaml
services:
  clickhouse:
    image: clickhouse/clickhouse-server:latest
    ports:
      - "8123:8123"
      - "9000:9000"
    environment:
      CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT: "1"
    volumes:
      - clickhouse_data:/var/lib/clickhouse
    ulimits:
      nofile:
        soft: 262144
        hard: 262144
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:8123/ping"]
      interval: 5s
      timeout: 5s
      retries: 20

  server:
    build:
      context: ./server
    depends_on:
      clickhouse:
        condition: service_healthy
    ports:
      - "8080:8080"
    command:
      - "-clickhouse=clickhouse://clickhouse:9000/default"
      - "-auto-update"
      - "-auto-update-interval=720h"
      - "-db-cache=/data/hyoketsu.db"
    volumes:
      - server_data:/data
    restart: unless-stopped

volumes:
  clickhouse_data:
  server_data:
```

`-auto-update-interval=720h` is 30 days. `restart: unless-stopped` covers the case where the server starts before ClickHouse's healthcheck has ever passed (Compose retries per `depends_on`, but `restart` is the backstop if the container still exits).

- [ ] **Step 3: Validate compose syntax**

Run: `docker compose config`
Expected: prints the fully-resolved compose config with no errors (no ClickHouse or server processes are actually started — this only validates YAML + schema).

- [ ] **Step 4: Commit**

```bash
git add server/Dockerfile docker-compose.yml
git commit -m "feat(docker): wire server into docker-compose with auto-update enabled"
```

---

### Task 6: README updates

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add `extract --remote` example**

In the `## Extract` section (around line 96–108), after the existing `--dotnet-only --dedup` example, add:

```
old:
# Only .NET, skip dupes
./hyoketsu extract --dotnet-only --dedup /path/to/binaries /path/to/output
```
```
new:
# Only .NET, skip dupes
./hyoketsu extract --dotnet-only --dedup /path/to/binaries /path/to/output

# Extract against a remote server (ClickHouse backend) instead of a local db
./hyoketsu extract --remote http://host:8080 /path/to/binaries /path/to/output
```

- [ ] **Step 2: Document server auto-update in the `## Server` section**

Replace the `## Server` section (lines 88–94):

```
old:
## Server

The `server/` directory contains a ClickHouse-backed HTTP server for centralized scanning. See `docker-compose.yml` to get started.

```
cd server && go build -o server .
```
```
```
new:
## Server

The `server/` directory contains a ClickHouse-backed HTTP server for centralized scanning, exposing `POST /lookup` and `GET /stats`.

```
cd server && go build -o server .
```

Run `docker compose up` to bring up ClickHouse and the server together. The server is started with `-auto-update`, so it downloads the latest prebuilt db from the Assetnote CDN and re-imports it into ClickHouse on startup and then every `-auto-update-interval` (default 720h / 30 days) — no manual `-import-sqlite` step is needed.

Flags:

- `-listen` — HTTP listen address (default `:8080`)
- `-clickhouse` — ClickHouse DSN (default `clickhouse://localhost:9000/default`)
- `-import-sqlite <path>` — one-shot import from a local SQLite db, then exit
- `-import-nuget <dir>` — one-shot import of NuGet JSONL data, then exit
- `-auto-update` — periodically download the latest prebuilt db and re-import it
- `-auto-update-interval` — interval between re-imports, used with `-auto-update` (default 720h)
- `-db-cache` — local path to cache the downloaded db before importing, used with `-auto-update` (default `/data/hyoketsu.db`)
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document extract --remote and server auto-update"
```

---

### Task 7: Full verification and PR prep

**Files:** none (verification only)

- [ ] **Step 1: Build and vet both modules**

```bash
go build ./...
go vet ./...
cd server && go build ./... && go vet ./...
cd -
```
Expected: no errors from either module.

- [ ] **Step 2: Run full test suites**

```bash
go test ./...
cd server && go test ./...
cd -
```
Expected: PASS for both modules.

- [ ] **Step 3: Re-validate docker-compose**

```bash
docker compose config
```
Expected: valid, no errors.

- [ ] **Step 4: Review the branch's full diff and commit log**

```bash
git log --oneline main..HEAD
git diff main...HEAD --stat
```
Confirm every task's commit is present and the diff only touches the files listed in this plan.

- [ ] **Step 5: Hand back to the user**

Do not push the branch or open the PR automatically — report the branch name, commit log, and test results, and ask whether to push + open the PR (pushing/opening a PR is a visible, hard-to-reverse action per the operating rules).
