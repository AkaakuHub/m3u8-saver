package hls

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type MasterPlaylist struct {
	AudioURI        string
	AudioLine       string
	VideoURI        string
	VideoStreamLine string
}

type MediaPlaylist struct {
	MediaURIs []string
}

func ParseMaster(body []byte) (MasterPlaylist, error) {
	if !IsPlaylist(body) {
		return MasterPlaylist{}, fmt.Errorf("master playlist is not a valid m3u8 file")
	}

	// Observed master playlists use multiple audio and video variants for quality levels.
	// Audio selection is strict: prefer the single DEFAULT=YES track, otherwise require a single track.
	// Video selection is strict: choose the variant with the highest BANDWIDTH.
	scanner := bufio.NewScanner(bytes.NewReader(body))
	audioCandidates := make([]audioCandidate, 0, 2)
	defaultAudioCandidates := make([]audioCandidate, 0, 1)
	var videoURI string
	var videoStreamLine string
	var highestBandwidth int
	expectVideoURI := false
	currentBandwidth := 0
	currentStreamLine := ""

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
			candidate := audioCandidate{
				URI:  uri,
				Line: line,
			}
			audioCandidates = append(audioCandidates, candidate)
			if strings.Contains(line, "DEFAULT=YES") {
				defaultAudioCandidates = append(defaultAudioCandidates, candidate)
			}
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			bandwidth, err := readIntAttribute(line, "BANDWIDTH")
			if err != nil {
				return MasterPlaylist{}, err
			}
			currentBandwidth = bandwidth
			currentStreamLine = line
			expectVideoURI = true
			continue
		}

		if expectVideoURI && !strings.HasPrefix(line, "#") {
			if videoURI == "" || currentBandwidth > highestBandwidth {
				videoURI = line
				videoStreamLine = currentStreamLine
				highestBandwidth = currentBandwidth
			}
			expectVideoURI = false
		}
	}

	if err := scanner.Err(); err != nil {
		return MasterPlaylist{}, fmt.Errorf("failed to read master playlist: %w", err)
	}
	audio, err := selectAudioURI(audioCandidates, defaultAudioCandidates)
	if err != nil {
		return MasterPlaylist{}, err
	}
	if audio.URI == "" {
		return MasterPlaylist{}, fmt.Errorf("audio playlist URI was not found in master playlist")
	}
	if videoURI == "" {
		return MasterPlaylist{}, fmt.Errorf("video playlist URI was not found in master playlist")
	}
	if videoStreamLine == "" {
		return MasterPlaylist{}, fmt.Errorf("video stream info was not found in master playlist")
	}

	return MasterPlaylist{
		AudioURI:        audio.URI,
		AudioLine:       audio.Line,
		VideoURI:        videoURI,
		VideoStreamLine: videoStreamLine,
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

type audioCandidate struct {
	URI  string
	Line string
}

func selectAudioURI(audioCandidates, defaultAudioCandidates []audioCandidate) (audioCandidate, error) {
	switch {
	case len(defaultAudioCandidates) == 1:
		return defaultAudioCandidates[0], nil
	case len(defaultAudioCandidates) > 1:
		return audioCandidate{}, fmt.Errorf("multiple default audio playlists were found in master playlist")
	case len(audioCandidates) == 1:
		return audioCandidates[0], nil
	case len(audioCandidates) == 0:
		return audioCandidate{}, nil
	default:
		return audioCandidate{}, fmt.Errorf("audio playlist selection is ambiguous without DEFAULT=YES")
	}
}

func readIntAttribute(line, key string) (int, error) {
	value, err := readAttributeValue(line, key)
	if err != nil {
		return 0, err
	}

	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s attribute is not a valid integer in line: %s", key, line)
	}

	return number, nil
}

func readAttributeValue(line, key string) (string, error) {
	pattern := key + "="
	start := strings.Index(line, pattern)
	if start == -1 {
		return "", fmt.Errorf("%s attribute was not found in line: %s", key, line)
	}

	valueStart := start + len(pattern)
	if valueStart >= len(line) {
		return "", fmt.Errorf("%s attribute is empty in line: %s", key, line)
	}

	if line[valueStart] == '"' {
		valueEnd := strings.Index(line[valueStart+1:], "\"")
		if valueEnd == -1 {
			return "", fmt.Errorf("%s attribute is not closed in line: %s", key, line)
		}
		return line[valueStart+1 : valueStart+1+valueEnd], nil
	}

	valueEnd := strings.Index(line[valueStart:], ",")
	if valueEnd == -1 {
		return line[valueStart:], nil
	}

	return line[valueStart : valueStart+valueEnd], nil
}

func BuildSingleVariantMaster(master MasterPlaylist) []byte {
	return []byte(strings.Join([]string{
		"#EXTM3U",
		master.AudioLine,
		master.VideoStreamLine,
		master.VideoURI,
		"",
	}, "\n"))
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
