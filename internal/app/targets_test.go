package app

import (
	"os"
	"path/filepath"
	"testing"

	"m3u8-saver/internal/config"
)

func TestBuildTargetsIncludesArchiveVideoURLsAndDatePredictions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.json")
	body := `[
		{"video_url": "/archive/hls/20251113-2nd/index.m3u8", "name": "second"},
		{"video_url": "/archive/hls/17a5a73f-4723-4a21-a518-17f7a61be8b7/2026-01-14T2018/hls/index.m3u8"},
		{"video_url": "/archive/hls/20260101/index.m3u8"}
	]`
	if err := os.WriteFile(archivePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	targets, err := buildTargets(config.Config{
		BaseURL:           "https://example.com",
		ArchiveJSONPath:   archivePath,
		StartDate:         "20260101",
		EndDate:           "20260102",
		OutDir:            dir,
		RetryCount:        1,
		Parallelism:       1,
		RequestTimeoutSec: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(targets) != 4 {
		t.Fatalf("expected 4 targets, got %d: %#v", len(targets), targets)
	}

	assertTarget(t, targets[0], 0, "20251113-2nd", "https://example.com/archive/hls/20251113-2nd/index.m3u8")
	assertTarget(t, targets[1], 1, "17a5a73f-4723-4a21-a518-17f7a61be8b7/2026-01-14T2018/hls", "https://example.com/archive/hls/17a5a73f-4723-4a21-a518-17f7a61be8b7/2026-01-14T2018/hls/index.m3u8")
	assertTarget(t, targets[2], 2, "20260101", "https://example.com/archive/hls/20260101/index.m3u8")
	assertTarget(t, targets[3], 3, "20260102", "https://example.com/archive/hls/20260102/index.m3u8")
}

func TestLocalDirFromVideoURLRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	if _, err := localDirFromVideoURL("/"); err == nil {
		t.Fatal("expected error")
	}
}

func assertTarget(t *testing.T, current target, index int, key, targetURL string) {
	t.Helper()

	if current.Index != index {
		t.Fatalf("expected index %d, got %d", index, current.Index)
	}
	if current.Label != key || current.StateKey != key || current.LocalDir != key {
		t.Fatalf("expected key %s, got label=%s stateKey=%s localDir=%s", key, current.Label, current.StateKey, current.LocalDir)
	}
	if current.URL != targetURL {
		t.Fatalf("expected URL %s, got %s", targetURL, current.URL)
	}
}
