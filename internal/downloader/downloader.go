package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"m3u8-saver/internal/ui"
)

var ErrNotFound = errors.New("resource not found")

type Client struct {
	httpClient *http.Client
	retries    int
}

type ResourceMetadata struct {
	ContentLength int64
}

type ProgressReporter interface {
	NewProxyReader(label string, total int64, reader io.ReadCloser) *ui.ProxyReader
	Complete(label string, total int64)
}

func New(timeout time.Duration, retries int) *Client {
	return &Client{
		httpClient: &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout: timeout,
				}).DialContext,
				ResponseHeaderTimeout: timeout,
				TLSHandshakeTimeout:   timeout,
			},
		},
		retries: retries,
	}
}

func (c *Client) Fetch(ctx context.Context, resourceURL string) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= c.retries; attempt++ {
		body, retry, err := c.fetchOnce(ctx, resourceURL)
		if err == nil {
			return body, nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}

		lastErr = err
		if !retry || attempt == c.retries {
			break
		}
	}

	return nil, fmt.Errorf("failed to fetch %s: %w", resourceURL, lastErr)
}

func (c *Client) Head(ctx context.Context, resourceURL string) (ResourceMetadata, error) {
	var lastErr error

	for attempt := 0; attempt <= c.retries; attempt++ {
		metadata, retry, err := c.headOnce(ctx, resourceURL)
		if err == nil {
			return metadata, nil
		}
		if errors.Is(err, ErrNotFound) {
			return ResourceMetadata{}, err
		}

		lastErr = err
		if !retry || attempt == c.retries {
			break
		}
	}

	return ResourceMetadata{}, fmt.Errorf("failed to head %s: %w", resourceURL, lastErr)
}

func (c *Client) DownloadToFile(ctx context.Context, resourceURL, destinationPath string, expectedSize int64, progress ProgressReporter, progressLabel string) error {
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

	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		retry, err := c.downloadOnce(ctx, resourceURL, destinationPath, expectedSize, progress, progressLabel)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrNotFound) {
			return err
		}

		lastErr = err
		if !retry || attempt == c.retries {
			break
		}
	}

	return fmt.Errorf("failed to download %s: %w", resourceURL, lastErr)
}

func (c *Client) fetchOnce(ctx context.Context, resourceURL string) ([]byte, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create request for %s: %w", resourceURL, err)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, true, err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return nil, false, ErrNotFound
	}
	if isRetryableStatus(response.StatusCode) {
		return nil, true, fmt.Errorf("received retryable status %d for %s", response.StatusCode, resourceURL)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, false, fmt.Errorf("received status %d for %s", response.StatusCode, resourceURL)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, true, fmt.Errorf("failed to read response body for %s: %w", resourceURL, err)
	}

	return body, false, nil
}

func (c *Client) headOnce(ctx context.Context, resourceURL string) (ResourceMetadata, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, resourceURL, nil)
	if err != nil {
		return ResourceMetadata{}, false, fmt.Errorf("failed to create request for %s: %w", resourceURL, err)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return ResourceMetadata{}, true, err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		return ResourceMetadata{}, false, ErrNotFound
	}
	if isRetryableStatus(response.StatusCode) {
		return ResourceMetadata{}, true, fmt.Errorf("received retryable status %d for %s", response.StatusCode, resourceURL)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ResourceMetadata{}, false, fmt.Errorf("received status %d for %s", response.StatusCode, resourceURL)
	}
	if response.ContentLength < 0 {
		return ResourceMetadata{}, false, fmt.Errorf("content-length header is missing for %s", resourceURL)
	}

	return ResourceMetadata{ContentLength: response.ContentLength}, false, nil
}

func (c *Client) downloadOnce(ctx context.Context, resourceURL, destinationPath string, expectedSize int64, progress ProgressReporter, progressLabel string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request for %s: %w", resourceURL, err)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return true, err
	}

	if response.StatusCode == http.StatusNotFound {
		_ = response.Body.Close()
		return false, ErrNotFound
	}
	if isRetryableStatus(response.StatusCode) {
		_ = response.Body.Close()
		return true, fmt.Errorf("received retryable status %d for %s", response.StatusCode, resourceURL)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		return false, fmt.Errorf("received status %d for %s", response.StatusCode, resourceURL)
	}

	var reader io.ReadCloser = response.Body
	var progressReader *ui.ProxyReader
	if progress != nil {
		progressReader = progress.NewProxyReader(progressLabel, expectedSize, response.Body)
		reader = progressReader
	}
	defer reader.Close()

	tempPath := destinationPath + ".tmp"
	file, err := os.Create(tempPath)
	if err != nil {
		return false, fmt.Errorf("failed to create temp file %s: %w", tempPath, err)
	}

	written, err := io.Copy(file, reader)
	if err != nil {
		if progressReader != nil {
			progressReader.Abort()
		}
		_ = file.Close()
		_ = os.Remove(tempPath)
		return true, fmt.Errorf("failed to write temp file %s: %w", tempPath, err)
	}
	if written != expectedSize {
		if progressReader != nil {
			progressReader.Abort()
		}
		_ = file.Close()
		_ = os.Remove(tempPath)
		return true, fmt.Errorf("downloaded size mismatch for %s: expected=%d actual=%d", resourceURL, expectedSize, written)
	}

	if err := file.Close(); err != nil {
		if progressReader != nil {
			progressReader.Abort()
		}
		_ = os.Remove(tempPath)
		return false, fmt.Errorf("failed to close temp file %s: %w", tempPath, err)
	}

	if err := os.Rename(tempPath, destinationPath); err != nil {
		if progressReader != nil {
			progressReader.Abort()
		}
		_ = os.Remove(tempPath)
		return false, fmt.Errorf("failed to move temp file to %s: %w", destinationPath, err)
	}
	if progress != nil {
		if progressReader != nil {
			progressReader.Wait()
		}
		progress.Complete(progressLabel, expectedSize)
	}

	return false, nil
}

func isRetryableStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooEarly ||
		statusCode >= 500
}
