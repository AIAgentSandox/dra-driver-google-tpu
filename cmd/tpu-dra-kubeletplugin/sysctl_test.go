package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyNetworkSettings(t *testing.T) {
	t.Run("writes succeed and read-back matches", func(t *testing.T) {
		root := t.TempDir()
		for _, s := range networkSettings {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, s.FilePath)), 0755); err != nil {
				t.Fatalf("failed to create dir for %s: %v", s.FilePath, err)
			}
		}

		if err := applyNetworkSettings(root); err != nil {
			t.Fatalf("applyNetworkSettings() returned error, want nil: %v", err)
		}

		for _, s := range networkSettings {
			got, err := os.ReadFile(filepath.Join(root, s.FilePath))
			if err != nil {
				t.Errorf("failed to read %s: %v", s.FilePath, err)
				continue
			}
			if strings.TrimSpace(string(got)) != s.Value {
				t.Errorf("%s = %q, want %q", s.FilePath, strings.TrimSpace(string(got)), s.Value)
			}
		}
	})

	t.Run("continues after a failed write and aggregates failures", func(t *testing.T) {
		root := t.TempDir()
		for _, s := range networkSettings {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, s.FilePath)), 0755); err != nil {
				t.Fatalf("failed to create dir for %s: %v", s.FilePath, err)
			}
		}
		// A directory sits in the file's place, so that write fails.
		failPaths := []string{
			filepath.Join(root, networkSettings[0].FilePath),
			filepath.Join(root, networkSettings[1].FilePath),
		}
		for _, p := range failPaths {
			if err := os.MkdirAll(p, 0755); err != nil {
				t.Fatalf("failed to block path %s: %v", p, err)
			}
		}

		err := applyNetworkSettings(root)
		if err == nil {
			t.Fatal("applyNetworkSettings() = nil, want aggregated error")
		}
		for _, p := range failPaths {
			if !strings.Contains(err.Error(), p) {
				t.Errorf("error %q does not report failed path %q", err.Error(), p)
			}
		}

		// Settings after the failed ones must still be written.
		for _, s := range networkSettings[2:] {
			got, readErr := os.ReadFile(filepath.Join(root, s.FilePath))
			if readErr != nil {
				t.Errorf("setting %s not written after earlier failures: %v", s.FilePath, readErr)
				continue
			}
			if strings.TrimSpace(string(got)) != s.Value {
				t.Errorf("setting %s = %q, want %q", s.FilePath, strings.TrimSpace(string(got)), s.Value)
			}
		}
	})
}
