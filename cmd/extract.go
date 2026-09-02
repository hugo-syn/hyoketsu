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
