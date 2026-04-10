package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"m3u8-saver/internal/config"
	"m3u8-saver/internal/date"
	"m3u8-saver/internal/downloader"
	"m3u8-saver/internal/hls"
	"m3u8-saver/internal/notify"
	"m3u8-saver/internal/state"
	"m3u8-saver/internal/ui"
)

type App struct {
	config   config.Config
	output   io.Writer
	client   *downloader.Client
	notifier *notify.DiscordWebhook
	progress *ui.Progress
	state    *state.Store
}

type dateResult struct {
	Index  int
	Date   string
	Status string
	Err    error
}

type counters struct {
	Processed int
	Succeeded int
	Failed    int
	Skipped   int
}

type filePlan struct {
	RemoteURL    string
	LocalPath    string
	Body         []byte
	ExpectedSize int64
}

type remotePlan struct {
	Master        filePlan
	AudioPlaylist filePlan
	VideoPlaylist filePlan
	AudioMedia    []filePlan
	VideoMedia    []filePlan
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
	total, err := date.Count(a.config.StartDate, a.config.EndDate)
	if err != nil {
		return err
	}

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

	jobs := make(chan string)
	results := make(chan dateResult)

	var workers sync.WaitGroup
	for workerIndex := 0; workerIndex < a.config.Parallelism; workerIndex++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for day := range jobs {
				results <- a.processDate(ctx, day)
			}
		}()
	}

	go func() {
		index := 0
		_ = date.Each(a.config.StartDate, a.config.EndDate, func(day string) error {
			jobs <- fmt.Sprintf("%d:%s", index, day)
			index++
			return nil
		})
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
		case "success":
			counts.Succeeded++
		case "skipped":
			counts.Skipped++
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
			case "success":
				fmt.Fprintln(a.output, ui.SuccessLabel(pendingResult.Date, "success"))
			case "skipped":
				fmt.Fprintln(a.output, ui.SkippedLabel(pendingResult.Date, "skipped"))
			default:
				fmt.Fprintln(a.output, ui.FailedLabel(pendingResult.Date, pendingResult.Err))
			}

			delete(pendingResults, nextResultIndex)
			nextResultIndex++
		}

		if a.shouldSendPeriodicDiscord(counts.Succeeded) {
			a.sendDiscordSafely(ctx, a.plainSummaryLine("progress", counts, total))
		}
	}

	if a.progress != nil {
		a.progress.Wait()
	}

	fmt.Fprintln(a.output, a.summaryLine("completed", counts, total))

	if counts.Succeeded > 0 {
		a.sendDiscordSafely(ctx, a.plainSummaryLine("completed", counts, total))
	}

	if counts.Failed > 0 {
		return fmt.Errorf("completed with %d failed dates", counts.Failed)
	}

	return nil
}

func (a *App) processDate(ctx context.Context, day string) dateResult {
	index, dateText, err := parseJob(day)
	if err != nil {
		return dateResult{Date: day, Status: "failed", Err: err}
	}

	targetURL := strings.ReplaceAll(a.config.URLTemplate, "{yyyymmdd}", dateText)

	if a.config.DryRun {
		return a.processDryRun(ctx, index, dateText, targetURL)
	}

	alreadyArchived, err := a.state.Has(dateText)
	if err != nil {
		return dateResult{Index: index, Date: dateText, Status: "failed", Err: err}
	}
	if alreadyArchived {
		return dateResult{Index: index, Date: dateText, Status: "skipped"}
	}

	plan, err := a.buildRemotePlan(ctx, targetURL)
	if err != nil {
		if errors.Is(err, downloader.ErrNotFound) {
			return dateResult{Index: index, Date: dateText, Status: "skipped"}
		}
		return dateResult{Index: index, Date: dateText, Status: "failed", Err: err}
	}

	if err := a.saveDate(ctx, dateText, plan); err != nil {
		return dateResult{Index: index, Date: dateText, Status: "failed", Err: err}
	}
	if err := a.state.Mark(dateText); err != nil {
		return dateResult{Index: index, Date: dateText, Status: "failed", Err: err}
	}

	return dateResult{Index: index, Date: dateText, Status: "success"}
}

func (a *App) processDryRun(ctx context.Context, index int, day, targetURL string) dateResult {
	body, err := a.client.Fetch(ctx, targetURL)
	if err != nil {
		if errors.Is(err, downloader.ErrNotFound) {
			return dateResult{Index: index, Date: day, Status: "skipped"}
		}
		return dateResult{Index: index, Date: day, Status: "failed", Err: err}
	}
	if !hls.IsPlaylist(body) {
		return dateResult{Index: index, Date: day, Status: "failed", Err: fmt.Errorf("index.m3u8 is not a valid playlist")}
	}

	return dateResult{Index: index, Date: day, Status: "success"}
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
	localMasterBody := hls.BuildSingleVariantMaster(masterPlaylist)

	audioURL, err := resolveURL(masterURL, masterPlaylist.AudioURI)
	if err != nil {
		return remotePlan{}, err
	}
	videoURL, err := resolveURL(masterURL, masterPlaylist.VideoURI)
	if err != nil {
		return remotePlan{}, err
	}

	audioBody, err := a.client.Fetch(ctx, audioURL)
	if err != nil {
		return remotePlan{}, err
	}
	videoBody, err := a.client.Fetch(ctx, videoURL)
	if err != nil {
		return remotePlan{}, err
	}

	audioPlaylist, err := hls.ParseMedia(audioBody)
	if err != nil {
		return remotePlan{}, err
	}
	videoPlaylist, err := hls.ParseMedia(videoBody)
	if err != nil {
		return remotePlan{}, err
	}

	audioMediaURLs, err := resolveMany(audioURL, audioPlaylist.MediaURIs)
	if err != nil {
		return remotePlan{}, err
	}
	videoMediaURLs, err := resolveMany(videoURL, videoPlaylist.MediaURIs)
	if err != nil {
		return remotePlan{}, err
	}

	audioPlaylistLocalPath, err := localPathFromReference(masterPlaylist.AudioURI)
	if err != nil {
		return remotePlan{}, err
	}
	videoPlaylistLocalPath, err := localPathFromReference(masterPlaylist.VideoURI)
	if err != nil {
		return remotePlan{}, err
	}
	audioMediaFiles, err := a.buildMediaFilePlans(ctx, audioPlaylist.MediaURIs, audioMediaURLs)
	if err != nil {
		return remotePlan{}, err
	}
	videoMediaFiles, err := a.buildMediaFilePlans(ctx, videoPlaylist.MediaURIs, videoMediaURLs)
	if err != nil {
		return remotePlan{}, err
	}

	return remotePlan{
		Master: filePlan{
			RemoteURL:    masterURL,
			LocalPath:    "index.m3u8",
			Body:         localMasterBody,
			ExpectedSize: int64(len(localMasterBody)),
		},
		AudioPlaylist: filePlan{
			RemoteURL:    audioURL,
			LocalPath:    audioPlaylistLocalPath,
			Body:         audioBody,
			ExpectedSize: int64(len(audioBody)),
		},
		VideoPlaylist: filePlan{
			RemoteURL:    videoURL,
			LocalPath:    videoPlaylistLocalPath,
			Body:         videoBody,
			ExpectedSize: int64(len(videoBody)),
		},
		AudioMedia: audioMediaFiles,
		VideoMedia: videoMediaFiles,
	}, nil
}

func (a *App) saveDate(ctx context.Context, day string, plan remotePlan) error {
	dayDir := filepath.Join(a.config.OutDir, day)
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		return fmt.Errorf("failed to create day directory %s: %w", dayDir, err)
	}

	requiredFiles := make([]string, 0)

	if err := a.writeIfMissing(filepath.Join(dayDir, plan.Master.LocalPath), plan.Master.Body, plan.Master.ExpectedSize); err != nil {
		return err
	}
	requiredFiles = append(requiredFiles, filepath.Join(dayDir, plan.Master.LocalPath))

	if err := a.writeIfMissing(filepath.Join(dayDir, plan.AudioPlaylist.LocalPath), plan.AudioPlaylist.Body, plan.AudioPlaylist.ExpectedSize); err != nil {
		return err
	}
	requiredFiles = append(requiredFiles, filepath.Join(dayDir, plan.AudioPlaylist.LocalPath))

	if err := a.writeIfMissing(filepath.Join(dayDir, plan.VideoPlaylist.LocalPath), plan.VideoPlaylist.Body, plan.VideoPlaylist.ExpectedSize); err != nil {
		return err
	}
	requiredFiles = append(requiredFiles, filepath.Join(dayDir, plan.VideoPlaylist.LocalPath))

	audioFiles, err := a.downloadMediaFiles(ctx, dayDir, plan.AudioMedia)
	if err != nil {
		return err
	}
	videoFiles, err := a.downloadMediaFiles(ctx, dayDir, plan.VideoMedia)
	if err != nil {
		return err
	}

	requiredFiles = append(requiredFiles, audioFiles...)
	requiredFiles = append(requiredFiles, videoFiles...)

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

func (a *App) downloadMediaFiles(ctx context.Context, dayDir string, files []filePlan) ([]string, error) {
	localPaths := make([]string, 0, len(files))
	for _, file := range files {
		destinationPath := filepath.Join(dayDir, filepath.FromSlash(file.LocalPath))
		progressLabel := fmt.Sprintf("%s/%s", filepath.Base(dayDir), file.LocalPath)
		if err := a.client.DownloadToFile(ctx, file.RemoteURL, destinationPath, file.ExpectedSize, a.progress, progressLabel); err != nil {
			return nil, err
		}

		localPaths = append(localPaths, destinationPath)
	}

	return localPaths, nil
}

func (a *App) shouldSendPeriodicDiscord(succeeded int) bool {
	return !a.config.DryRun &&
		a.notifier != nil &&
		a.config.Discord != nil &&
		succeeded > 0 &&
		succeeded%a.config.Discord.NotifyEvery == 0
}

func (a *App) summaryLine(prefix string, counts counters, total int) string {
	return ui.ProgressLine(prefix, counts.Processed, total, counts.Succeeded, counts.Failed, counts.Skipped)
}

func (a *App) plainSummaryLine(prefix string, counts counters, total int) string {
	return ui.PlainProgressLine(prefix, counts.Processed, total, counts.Succeeded, counts.Failed, counts.Skipped)
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
		localPath, err := localPathFromReference(reference)
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

func localPathFromReference(reference string) (string, error) {
	parsedURL, err := url.Parse(reference)
	if err != nil {
		return "", fmt.Errorf("failed to parse reference %s: %w", reference, err)
	}
	if parsedURL.IsAbs() {
		return "", fmt.Errorf("absolute reference is not supported without playlist rewrite: %s", reference)
	}
	if parsedURL.RawQuery != "" {
		return "", fmt.Errorf("reference with query string is not supported without playlist rewrite: %s", reference)
	}
	if parsedURL.Fragment != "" {
		return "", fmt.Errorf("reference with fragment is not supported: %s", reference)
	}

	cleanPath := path.Clean(strings.TrimPrefix(parsedURL.Path, "/"))
	if cleanPath == "." || cleanPath == "" {
		return "", fmt.Errorf("reference path is empty: %s", reference)
	}
	if strings.HasPrefix(cleanPath, "../") {
		return "", fmt.Errorf("reference path escapes output directory: %s", reference)
	}

	return cleanPath, nil
}

func parseJob(value string) (int, string, error) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid job value: %s", value)
	}

	index, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", fmt.Errorf("invalid job index: %w", err)
	}

	return index, parts[1], nil
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
			fmt.Fprintln(a.output, ui.SkippedLabel("discord", "rate limited"))
			return
		}
		fmt.Fprintln(a.output, ui.FailedLabel("discord", err))
	}
}
