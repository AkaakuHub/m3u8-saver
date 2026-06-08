package hls

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"path"
	"strings"
)

type MasterPlaylist struct {
	AudioURIs []string
	VideoURIs []string
}

func (p MasterPlaylist) References() []string {
	references := make([]string, 0, len(p.AudioURIs)+len(p.VideoURIs))
	references = append(references, p.AudioURIs...)
	references = append(references, p.VideoURIs...)

	return references
}

type MediaPlaylist struct {
	MediaURIs []string
}

func ParseMaster(body []byte) (MasterPlaylist, error) {
	if !IsPlaylist(body) {
		return MasterPlaylist{}, fmt.Errorf("master playlist is not a valid m3u8 file")
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))
	audioSeen := map[string]struct{}{}
	videoSeen := map[string]struct{}{}
	audioURIs := make([]string, 0)
	videoURIs := make([]string, 0)
	expectVideoURI := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-MEDIA:") && strings.Contains(line, "TYPE=AUDIO") {
			uri, err := readQuotedAttribute(line, "URI")
			if err != nil {
				return MasterPlaylist{}, err
			}
			audioURIs = appendIfMissing(audioURIs, audioSeen, uri)
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			expectVideoURI = true
			continue
		}

		if expectVideoURI && !strings.HasPrefix(line, "#") {
			videoURIs = appendIfMissing(videoURIs, videoSeen, line)
			expectVideoURI = false
		}
	}

	if err := scanner.Err(); err != nil {
		return MasterPlaylist{}, fmt.Errorf("failed to read master playlist: %w", err)
	}
	if len(audioURIs) == 0 {
		return MasterPlaylist{}, fmt.Errorf("audio playlist URI was not found in master playlist")
	}
	if len(videoURIs) == 0 {
		return MasterPlaylist{}, fmt.Errorf("video playlist URI was not found in master playlist")
	}

	return MasterPlaylist{
		AudioURIs: audioURIs,
		VideoURIs: videoURIs,
	}, nil
}

func ParseMedia(body []byte) (MediaPlaylist, error) {
	if !IsPlaylist(body) {
		return MediaPlaylist{}, fmt.Errorf("media playlist is not a valid m3u8 file")
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))
	seen := map[string]struct{}{}
	uris := make([]string, 0)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			uri, err := readQuotedAttribute(line, "URI")
			if err != nil {
				return MediaPlaylist{}, err
			}
			uris = appendIfMissing(uris, seen, uri)
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		uris = appendIfMissing(uris, seen, line)
	}

	if err := scanner.Err(); err != nil {
		return MediaPlaylist{}, fmt.Errorf("failed to read media playlist: %w", err)
	}
	if len(uris) == 0 {
		return MediaPlaylist{}, fmt.Errorf("media files were not found in media playlist")
	}

	return MediaPlaylist{MediaURIs: uris}, nil
}

func IsPlaylist(body []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(body), []byte("#EXTM3U"))
}

func appendIfMissing(items []string, seen map[string]struct{}, value string) []string {
	if _, exists := seen[value]; exists {
		return items
	}

	seen[value] = struct{}{}
	return append(items, value)
}

func readQuotedAttribute(line, key string) (string, error) {
	pattern := key + "=\""
	start := strings.Index(line, pattern)
	if start == -1 {
		return "", fmt.Errorf("%s attribute was not found in line: %s", key, line)
	}

	valueStart := start + len(pattern)
	valueEnd := strings.Index(line[valueStart:], "\"")
	if valueEnd == -1 {
		return "", fmt.Errorf("%s attribute is not closed in line: %s", key, line)
	}

	return line[valueStart : valueStart+valueEnd], nil
}

func LocalPathFromReference(reference string) (string, error) {
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
