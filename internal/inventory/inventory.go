package inventory

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"m3u8-saver/internal/hls"
	"m3u8-saver/internal/state"
	"m3u8-saver/internal/status"
	"m3u8-saver/internal/ui"
)

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

	targetDirectories, err := listTargetDirectories(outDir)
	if err != nil {
		return err
	}

	archivedCount := 0
	for _, targetDir := range targetDirectories {
		key, err := filepath.Rel(outDir, targetDir)
		if err != nil {
			return fmt.Errorf("failed to build target key: %w", err)
		}
		key = filepath.ToSlash(key)

		ok, err := isArchivedTargetDirectory(targetDir)
		if err != nil {
			fmt.Fprintln(output, ui.FailedLabel(key, err))
			continue
		}
		if !ok {
			fmt.Fprintln(output, ui.IncompleteLabel(key, status.Incomplete))
			continue
		}
		if err := store.Mark(key); err != nil {
			return err
		}
		archivedCount++
		fmt.Fprintln(output, ui.SuccessLabel(key, status.Success))
	}

	fmt.Fprintln(output, ui.InventorySummaryLine(archivedCount, len(targetDirectories)))

	return nil
}

func listTargetDirectories(outDir string) ([]string, error) {
	targetDirectories := make([]string, 0)
	err := filepath.WalkDir(outDir, func(currentPath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if _, err := os.Stat(filepath.Join(currentPath, "index.m3u8")); err == nil {
			targetDirectories = append(targetDirectories, currentPath)
			return filepath.SkipDir
		} else if !os.IsNotExist(err) {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to read outDir: %w", err)
	}

	sort.Strings(targetDirectories)
	return targetDirectories, nil
}

func isArchivedTargetDirectory(targetDir string) (bool, error) {
	masterBody, err := os.ReadFile(filepath.Join(targetDir, "index.m3u8"))
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

	for _, reference := range master.References() {
		playlistPath, err := hls.LocalPathFromReference(reference)
		if err != nil {
			return false, err
		}
		playlistBody, err := os.ReadFile(filepath.Join(targetDir, filepath.FromSlash(playlistPath)))
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed to read media playlist: %w", err)
		}
		media, err := hls.ParseMedia(playlistBody)
		if err != nil {
			return false, err
		}
		if ok, err := hasAllMedia(targetDir, media.MediaURIs); !ok || err != nil {
			return ok, err
		}
	}

	return true, nil
}

func hasAllMedia(targetDir string, references []string) (bool, error) {
	for _, reference := range references {
		localPath, err := hls.LocalPathFromReference(reference)
		if err != nil {
			return false, err
		}
		if _, err := os.Stat(filepath.Join(targetDir, filepath.FromSlash(localPath))); err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed to inspect media file: %w", err)
		}
	}

	return true, nil
}
