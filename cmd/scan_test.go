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
