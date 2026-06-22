package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"m3u8-saver/internal/config"
	"m3u8-saver/internal/date"
)

type target struct {
	Index    int
	Label    string
	StateKey string
	LocalDir string
	URL      string
}

type archiveEntry struct {
	VideoURL string `json:"video_url"`
}

func buildTargets(cfg config.Config) ([]target, error) {
	targets := make([]target, 0)
	seenURLs := map[string]struct{}{}
	seenKeys := map[string]struct{}{}

	if cfg.ArchiveJSONPath != "" {
		archiveTargets, err := readArchiveTargets(cfg)
		if err != nil {
			return nil, err
		}
		for _, current := range archiveTargets {
			targets = appendTarget(targets, current, seenURLs, seenKeys)
		}
	}

	if err := date.Each(cfg.StartDate, cfg.EndDate, func(day string) error {
		targetURL, err := futureTargetURL(cfg.BaseURL, day)
		if err != nil {
			return err
		}
		returned := target{
			Label:    day,
			StateKey: day,
			LocalDir: day,
			URL:      targetURL,
		}
		targets = appendTarget(targets, returned, seenURLs, seenKeys)
		return nil
	}); err != nil {
		return nil, err
	}

	for index := range targets {
		targets[index].Index = index
	}

	return targets, nil
}

func readArchiveTargets(cfg config.Config) ([]target, error) {
	body, err := os.ReadFile(cfg.ArchiveJSONPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read archiveJsonPath: %w", err)
	}

	var entries []archiveEntry
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("failed to parse archiveJsonPath: %w", err)
	}

	targets := make([]target, 0, len(entries))
	for _, entry := range entries {
		if entry.VideoURL == "" {
			continue
		}

		targetURL, err := resolveURL(cfg.BaseURL, entry.VideoURL)
		if err != nil {
			return nil, err
		}
		localDir, err := localDirFromVideoURL(entry.VideoURL)
		if err != nil {
			return nil, err
		}

		targets = append(targets, target{
			Label:    localDir,
			StateKey: localDir,
			LocalDir: localDir,
			URL:      targetURL,
		})
	}

	return targets, nil
}

func futureTargetURL(baseURL, day string) (string, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse baseUrl: %w", err)
	}
	parsedURL.Path = path.Join(parsedURL.Path, "archive/hls", day, "index.m3u8")
	parsedURL.RawQuery = ""
	parsedURL.Fragment = ""

	return parsedURL.String(), nil
}

func localDirFromVideoURL(videoURL string) (string, error) {
	parsedURL, err := url.Parse(videoURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse video_url %s: %w", videoURL, err)
	}

	cleanPath := path.Clean(strings.TrimPrefix(parsedURL.Path, "/"))
	cleanPath = strings.TrimSuffix(cleanPath, "/index.m3u8")
	cleanPath = strings.TrimPrefix(cleanPath, "archive/hls/")
	if cleanPath == "" || cleanPath == "." {
		return "", fmt.Errorf("video_url path is empty: %s", videoURL)
	}
	if strings.HasPrefix(cleanPath, "../") {
		return "", fmt.Errorf("video_url path escapes output directory: %s", videoURL)
	}

	return filepath.ToSlash(cleanPath), nil
}

func appendTarget(targets []target, current target, seenURLs, seenKeys map[string]struct{}) []target {
	if _, exists := seenURLs[current.URL]; exists {
		return targets
	}
	if _, exists := seenKeys[current.StateKey]; exists {
		return targets
	}

	seenURLs[current.URL] = struct{}{}
	seenKeys[current.StateKey] = struct{}{}
	return append(targets, current)
}
