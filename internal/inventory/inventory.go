package inventory

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"m3u8-saver/internal/hls"
	"m3u8-saver/internal/state"
	"m3u8-saver/internal/ui"
)

var dateDirectoryPattern = regexp.MustCompile(`^\d{8}$`)

func Run(outDir string, output io.Writer) error {
	ui.ConfigureColor(output)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("failed to create outDir: %w", err)
	}

	store, err := state.Open(outDir)
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()

	if err := store.Reset(); err != nil {
		return err
	}

	dateDirectories, err := listDateDirectories(outDir)
	if err != nil {
		return err
	}

	archivedCount := 0
	for _, dateDir := range dateDirectories {
		date := filepath.Base(dateDir)
		ok, err := isArchivedDateDirectory(dateDir)
		if err != nil {
			fmt.Fprintln(output, ui.FailedLabel(date, err))
			continue
		}
		if !ok {
			fmt.Fprintln(output, ui.IncompleteLabel(date, "incomplete"))
			continue
		}
		if err := store.Mark(date); err != nil {
			return err
		}
		archivedCount++
		fmt.Fprintln(output, ui.SuccessLabel(date, "success"))
	}

	fmt.Fprintln(output, ui.InventorySummaryLine(archivedCount, len(dateDirectories)))

	return nil
}

func listDateDirectories(outDir string) ([]string, error) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read outDir: %w", err)
	}

	dateDirectories := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() || !dateDirectoryPattern.MatchString(entry.Name()) {
			continue
		}
		dateDirectories = append(dateDirectories, filepath.Join(outDir, entry.Name()))
	}

	sort.Strings(dateDirectories)
	return dateDirectories, nil
}

func isArchivedDateDirectory(dateDir string) (bool, error) {
	masterBody, err := os.ReadFile(filepath.Join(dateDir, "index.m3u8"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read index.m3u8: %w", err)
	}

	master, err := hls.ParseMaster(masterBody)
	if err != nil {
		return false, err
	}

	audioPlaylistPath, err := hls.LocalPathFromReference(master.AudioURI)
	if err != nil {
		return false, err
	}
	videoPlaylistPath, err := hls.LocalPathFromReference(master.VideoURI)
	if err != nil {
		return false, err
	}

	audioPlaylistBody, err := os.ReadFile(filepath.Join(dateDir, filepath.FromSlash(audioPlaylistPath)))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read audio playlist: %w", err)
	}
	videoPlaylistBody, err := os.ReadFile(filepath.Join(dateDir, filepath.FromSlash(videoPlaylistPath)))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read video playlist: %w", err)
	}

	audioMedia, err := hls.ParseMedia(audioPlaylistBody)
	if err != nil {
		return false, err
	}
	videoMedia, err := hls.ParseMedia(videoPlaylistBody)
	if err != nil {
		return false, err
	}

	if ok, err := hasAllMedia(dateDir, audioMedia.MediaURIs); !ok || err != nil {
		return ok, err
	}
	if ok, err := hasAllMedia(dateDir, videoMedia.MediaURIs); !ok || err != nil {
		return ok, err
	}

	return true, nil
}

func hasAllMedia(dateDir string, references []string) (bool, error) {
	for _, reference := range references {
		localPath, err := hls.LocalPathFromReference(reference)
		if err != nil {
			return false, err
		}
		if _, err := os.Stat(filepath.Join(dateDir, filepath.FromSlash(localPath))); err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed to inspect media file: %w", err)
		}
	}

	return true, nil
}
