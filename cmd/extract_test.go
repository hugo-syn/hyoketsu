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
