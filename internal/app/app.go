package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"m3u8-saver/internal/config"
	"m3u8-saver/internal/downloader"
	"m3u8-saver/internal/hls"
	"m3u8-saver/internal/notify"
	"m3u8-saver/internal/state"
	"m3u8-saver/internal/status"
	"m3u8-saver/internal/ui"
)

type App struct {
	config   config.Config
	output   io.Writer
	client   *downloader.Client
	notifier *notify.DiscordWebhook
	progress *ui.Progress
	state    *state.Store
	buffered []string
}

type dateResult struct {
	Index  int
	Label  string
	Status status.Type
	Err    error
}

type counters struct {
	Processed int
	Succeeded int
	Failed    int
	Archived  int
	Missing   int
}

type filePlan struct {
	RemoteURL    string
	LocalPath    string
	Body         []byte
	ExpectedSize int64
}

type remotePlan struct {
	Master    filePlan
	Playlists []filePlan
	Media     []filePlan
}

func New(cfg config.Config, output io.Writer) (*App, error) {
	timeout := time.Duration(cfg.RequestTimeoutSec) * time.Second
	application := &App{
		config: cfg,
		output: output,
		client: downloader.New(timeout, cfg.RetryCount),
	}
	ui.ConfigureColor(output)

	if cfg.Discord != nil {
		application.notifier = notify.NewDiscordWebhook(cfg.Discord.WebhookURL, timeout)
	}

	return application, nil
}

func (a *App) Run(ctx context.Context) error {
	targets, err := buildTargets(a.config)
	if err != nil {
		return err
	}
	total := len(targets)

	if !a.config.DryRun {
		if err := a.initializePersistence(); err != nil {
			return err
		}
	}
	defer func() {
		if a.state != nil {
			_ = a.state.Close()
		}
	}()

	jobs := make(chan target)
	results := make(chan dateResult)

	var workers sync.WaitGroup
	for workerIndex := 0; workerIndex < a.config.Parallelism; workerIndex++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for current := range jobs {
				results <- a.processTarget(ctx, current)
			}
		}()
	}

	go func() {
		for _, current := range targets {
			jobs <- current
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()
	counts := counters{}
	pendingResults := map[int]dateResult{}
	nextResultIndex := 0

	for result := range results {
		counts.Processed++
		switch result.Status {
		case status.Success:
			counts.Succeeded++
		case status.Archived:
			counts.Archived++
		case status.Missing:
			counts.Missing++
		default:
			counts.Failed++
		}
		pendingResults[result.Index] = result

		for {
			pendingResult, exists := pendingResults[nextResultIndex]
			if !exists {
				break
			}

			switch pendingResult.Status {
			case status.Success:
				a.writeResultLine(ui.SuccessLabel(pendingResult.Label, status.Success))
			case status.Archived:
				a.writeResultLine(ui.ArchivedLabel(pendingResult.Label, status.Archived))
			case status.Missing:
				a.writeResultLine(ui.MissingLabel(pendingResult.Label, status.NotFound))
			default:
				a.writeResultLine(ui.FailedLabel(pendingResult.Label, pendingResult.Err))
			}

			delete(pendingResults, nextResultIndex)
			nextResultIndex++
		}
		a.flushBufferedLines()

		if a.shouldSendPeriodicDiscord(counts.Succeeded) {
			a.sendDiscordSafely(ctx, a.plainSummaryLine("progress", counts, total))
		}
	}

	if a.progress != nil {
		a.progress.Wait()
	}
	a.flushBufferedLines()

	fmt.Fprintln(a.output, a.summaryLine("completed", counts, total))

	if counts.Succeeded > 0 {
		a.sendDiscordSafely(ctx, a.plainSummaryLine("completed", counts, total))
	}

	if counts.Failed > 0 {
		return fmt.Errorf("completed with %d failed dates", counts.Failed)
	}

	return nil
}

func (a *App) processTarget(ctx context.Context, current target) dateResult {
	if a.config.DryRun {
		return a.processDryRun(ctx, current)
	}

	alreadyArchived, err := a.state.Has(current.StateKey)
	if err != nil {
		return dateResult{Index: current.Index, Label: current.Label, Status: status.Failed, Err: err}
	}
	plan, err := a.buildRemotePlan(ctx, current.URL)
	if err != nil {
		if errors.Is(err, downloader.ErrNotFound) {
			return dateResult{Index: current.Index, Label: current.Label, Status: status.Missing}
		}
		return dateResult{Index: current.Index, Label: current.Label, Status: status.Failed, Err: err}
	}
	if alreadyArchived {
		complete, err := a.hasSavedPlan(current, plan)
		if err != nil {
			return dateResult{Index: current.Index, Label: current.Label, Status: status.Failed, Err: err}
		}
		if complete {
			return dateResult{Index: current.Index, Label: current.Label, Status: status.Archived}
		}
	}
	if err := a.saveTarget(ctx, current, plan); err != nil {
		return dateResult{Index: current.Index, Label: current.Label, Status: status.Failed, Err: err}
	}
	if err := a.state.Mark(current.StateKey); err != nil {
		return dateResult{Index: current.Index, Label: current.Label, Status: status.Failed, Err: err}
	}

	return dateResult{Index: current.Index, Label: current.Label, Status: status.Success}
}

func (a *App) hasSavedPlan(current target, plan remotePlan) (bool, error) {
	targetDir := filepath.Join(a.config.OutDir, filepath.FromSlash(current.LocalDir))
	files := make([]filePlan, 0, 1+len(plan.Playlists)+len(plan.Media))
	files = append(files, plan.Master)
	files = append(files, plan.Playlists...)
	files = append(files, plan.Media...)

	for _, file := range files {
		info, err := os.Stat(filepath.Join(targetDir, filepath.FromSlash(file.LocalPath)))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("failed to inspect archived file: %w", err)
		}
		if info.Size() != file.ExpectedSize {
			return false, nil
		}
	}

	return true, nil
}

func (a *App) processDryRun(ctx context.Context, current target) dateResult {
	body, err := a.client.Fetch(ctx, current.URL)
	if err != nil {
		if errors.Is(err, downloader.ErrNotFound) {
			return dateResult{Index: current.Index, Label: current.Label, Status: status.Missing}
		}
		return dateResult{Index: current.Index, Label: current.Label, Status: status.Failed, Err: err}
	}
	if !hls.IsPlaylist(body) {
		return dateResult{Index: current.Index, Label: current.Label, Status: status.Failed, Err: fmt.Errorf("index.m3u8 is not a valid playlist")}
	}

	return dateResult{Index: current.Index, Label: current.Label, Status: status.Success}
}

func (a *App) buildRemotePlan(ctx context.Context, masterURL string) (remotePlan, error) {
	masterBody, err := a.client.Fetch(ctx, masterURL)
	if err != nil {
		return remotePlan{}, err
	}

	masterPlaylist, err := hls.ParseMaster(masterBody)
	if err != nil {
		return remotePlan{}, err
	}

	playlists, mediaFiles, err := a.buildPlaylistPlans(ctx, masterURL, masterPlaylist.References())
	if err != nil {
		return remotePlan{}, err
	}

	return remotePlan{
		Master: filePlan{
			RemoteURL:    masterURL,
			LocalPath:    "index.m3u8",
			Body:         masterBody,
			ExpectedSize: int64(len(masterBody)),
		},
		Playlists: playlists,
		Media:     mediaFiles,
	}, nil
}

func (a *App) saveTarget(ctx context.Context, current target, plan remotePlan) error {
	targetDir := filepath.Join(a.config.OutDir, filepath.FromSlash(current.LocalDir))
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	requiredFiles := make([]string, 0)

	if err := a.writeIfMissing(filepath.Join(targetDir, plan.Master.LocalPath), plan.Master.Body, plan.Master.ExpectedSize); err != nil {
		return err
	}
	requiredFiles = append(requiredFiles, filepath.Join(targetDir, plan.Master.LocalPath))

	for _, playlist := range plan.Playlists {
		if err := a.writeIfMissing(filepath.Join(targetDir, playlist.LocalPath), playlist.Body, playlist.ExpectedSize); err != nil {
			return err
		}
		requiredFiles = append(requiredFiles, filepath.Join(targetDir, playlist.LocalPath))
	}

	mediaFiles, err := a.downloadMediaFiles(ctx, targetDir, current.Label, plan.Media)
	if err != nil {
		return err
	}
	requiredFiles = append(requiredFiles, mediaFiles...)

	for _, filePath := range requiredFiles {
		if _, err := os.Stat(filePath); err != nil {
			return fmt.Errorf("required file is missing after save: %s", filePath)
		}
	}

	return nil
}

func (a *App) writeIfMissing(destinationPath string, body []byte, expectedSize int64) error {
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", destinationPath, err)
	}

	if info, err := os.Stat(destinationPath); err == nil {
		if info.Size() == expectedSize {
			return nil
		}
		if err := os.Remove(destinationPath); err != nil {
			return fmt.Errorf("failed to remove size-mismatched file %s: %w", destinationPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to inspect %s: %w", destinationPath, err)
	}

	tempPath := destinationPath + ".tmp"
	if err := os.WriteFile(tempPath, body, 0o644); err != nil {
		return fmt.Errorf("failed to write temp file %s: %w", tempPath, err)
	}

	if err := os.Rename(tempPath, destinationPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to move temp file to %s: %w", destinationPath, err)
	}
	if info, err := os.Stat(destinationPath); err != nil {
		return fmt.Errorf("failed to inspect written file %s: %w", destinationPath, err)
	} else if info.Size() != expectedSize {
		return fmt.Errorf("written file size mismatch for %s: expected=%d actual=%d", destinationPath, expectedSize, info.Size())
	}

	return nil
}

func (a *App) downloadMediaFiles(ctx context.Context, targetDir, label string, files []filePlan) ([]string, error) {
	localPaths := make([]string, 0, len(files))
	for _, file := range files {
		destinationPath := filepath.Join(targetDir, filepath.FromSlash(file.LocalPath))
		progressLabel := fmt.Sprintf("%s %s", label, file.LocalPath)
		if err := a.client.DownloadToFile(ctx, file.RemoteURL, destinationPath, file.ExpectedSize, a.progress, progressLabel); err != nil {
			return nil, err
		}

		localPaths = append(localPaths, destinationPath)
	}

	return localPaths, nil
}

func (a *App) buildPlaylistPlans(ctx context.Context, masterURL string, references []string) ([]filePlan, []filePlan, error) {
	playlists := make([]filePlan, 0, len(references))
	mediaFiles := make([]filePlan, 0)

	for _, reference := range references {
		playlistURL, err := resolveURL(masterURL, reference)
		if err != nil {
			return nil, nil, err
		}
		playlistBody, err := a.client.Fetch(ctx, playlistURL)
		if err != nil {
			return nil, nil, err
		}
		playlistLocalPath, err := hls.LocalPathFromReference(reference)
		if err != nil {
			return nil, nil, err
		}
		playlist, err := hls.ParseMedia(playlistBody)
		if err != nil {
			return nil, nil, err
		}
		mediaURLs, err := resolveMany(playlistURL, playlist.MediaURIs)
		if err != nil {
			return nil, nil, err
		}
		files, err := a.buildMediaFilePlans(ctx, playlist.MediaURIs, mediaURLs)
		if err != nil {
			return nil, nil, err
		}

		playlists = append(playlists, filePlan{
			RemoteURL:    playlistURL,
			LocalPath:    playlistLocalPath,
			Body:         playlistBody,
			ExpectedSize: int64(len(playlistBody)),
		})
		mediaFiles = append(mediaFiles, files...)
	}

	return playlists, mediaFiles, nil
}

func (a *App) shouldSendPeriodicDiscord(succeeded int) bool {
	return !a.config.DryRun &&
		a.notifier != nil &&
		a.config.Discord != nil &&
		succeeded > 0 &&
		succeeded%a.config.Discord.NotifyEvery == 0
}

func (a *App) summaryLine(prefix string, counts counters, total int) string {
	return ui.ProgressLine(prefix, counts.Processed, total, counts.Succeeded, counts.Failed, counts.Archived, counts.Missing)
}

func (a *App) plainSummaryLine(prefix string, counts counters, total int) string {
	return ui.PlainProgressLine(prefix, counts.Processed, total, counts.Succeeded, counts.Failed, counts.Archived, counts.Missing)
}

func resolveMany(baseURL string, values []string) ([]string, error) {
	resolved := make([]string, 0, len(values))
	for _, value := range values {
		resolvedURL, err := resolveURL(baseURL, value)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, resolvedURL)
	}

	return resolved, nil
}

func (a *App) buildMediaFilePlans(ctx context.Context, references, resolvedURLs []string) ([]filePlan, error) {
	if len(references) != len(resolvedURLs) {
		return nil, fmt.Errorf("references and resolved URLs length mismatch")
	}

	files := make([]filePlan, 0, len(references))
	for index, reference := range references {
		localPath, err := hls.LocalPathFromReference(reference)
		if err != nil {
			return nil, err
		}
		metadata, err := a.client.Head(ctx, resolvedURLs[index])
		if err != nil {
			return nil, err
		}

		files = append(files, filePlan{
			RemoteURL:    resolvedURLs[index],
			LocalPath:    localPath,
			ExpectedSize: metadata.ContentLength,
		})
	}

	return files, nil
}

func resolveURL(baseURL, reference string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse base url %s: %w", baseURL, err)
	}
	relative, err := url.Parse(reference)
	if err != nil {
		return "", fmt.Errorf("failed to parse relative url %s: %w", reference, err)
	}

	return base.ResolveReference(relative).String(), nil
}

func (a *App) initializePersistence() error {
	if err := os.MkdirAll(a.config.OutDir, 0o755); err != nil {
		return fmt.Errorf("failed to create outDir: %w", err)
	}
	if a.progress == nil {
		a.progress = ui.NewProgress(a.output)
	}
	if a.state != nil {
		return nil
	}

	store, err := state.Open(a.config.OutDir)
	if err != nil {
		return err
	}
	a.state = store

	return nil
}

func (a *App) sendDiscordSafely(ctx context.Context, content string) {
	if a.notifier == nil {
		return
	}

	if err := a.notifier.Send(ctx, content); err != nil {
		if errors.Is(err, notify.ErrRateLimited) {
			a.writeResultLine(ui.MissingLabel("discord", "rate limited"))
			return
		}
		a.writeResultLine(ui.FailedLabel("discord", err))
	}
}

func (a *App) writeResultLine(line string) {
	if a.progress != nil && a.progress.HasActive() {
		a.buffered = append(a.buffered, line)
		return
	}

	fmt.Fprintln(a.output, line)
}

func (a *App) flushBufferedLines() {
	if a.progress != nil && a.progress.HasActive() {
		return
	}

	for _, line := range a.buffered {
		fmt.Fprintln(a.output, line)
	}
	a.buffered = a.buffered[:0]
}
