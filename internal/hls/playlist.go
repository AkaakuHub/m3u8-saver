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
	AudioVariants []AudioVariant
	VideoVariants []VideoVariant
}

func (p MasterPlaylist) References() []string {
	references := make([]string, 0, len(p.AudioVariants)+len(p.VideoVariants))
	seen := map[string]struct{}{}
	for _, audio := range p.AudioVariants {
		references = appendIfMissing(references, seen, audio.URI)
	}
	for _, video := range p.VideoVariants {
		references = appendIfMissing(references, seen, video.URI)
	}

	return references
}

func (p MasterPlaylist) SingleVariantReferences() ([]string, error) {
	audio, err := p.SelectAudio()
	if err != nil {
		return nil, err
	}
	video, err := p.SelectVideo()
	if err != nil {
		return nil, err
	}

	return []string{audio.URI, video.URI}, nil
}

func (p MasterPlaylist) SelectAudio() (AudioVariant, error) {
	defaultAudio := make([]AudioVariant, 0, 1)
	for _, audio := range p.AudioVariants {
		if audio.Default {
			defaultAudio = append(defaultAudio, audio)
		}
	}

	switch {
	case len(defaultAudio) == 1:
		return defaultAudio[0], nil
	case len(defaultAudio) > 1:
		return AudioVariant{}, fmt.Errorf("multiple default audio playlists were found in master playlist")
	case len(p.AudioVariants) == 1:
		return p.AudioVariants[0], nil
	case len(p.AudioVariants) == 0:
		return AudioVariant{}, fmt.Errorf("audio playlist URI was not found in master playlist")
	default:
		return AudioVariant{}, fmt.Errorf("audio playlist selection is ambiguous without DEFAULT=YES")
	}
}

func (p MasterPlaylist) SelectVideo() (VideoVariant, error) {
	if len(p.VideoVariants) == 0 {
		return VideoVariant{}, fmt.Errorf("video playlist URI was not found in master playlist")
	}

	selected := p.VideoVariants[0]
	for _, video := range p.VideoVariants[1:] {
		if video.Bandwidth > selected.Bandwidth {
			selected = video
		}
	}

	return selected, nil
}

type AudioVariant struct {
	URI     string
	Line    string
	Default bool
}

type VideoVariant struct {
	URI        string
	StreamLine string
	Bandwidth  int
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
	audioVariants := make([]AudioVariant, 0)
	videoVariants := make([]VideoVariant, 0)
	expectVideoURI := false
	currentVideo := VideoVariant{}

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
			if _, exists := audioSeen[uri]; !exists {
				audioSeen[uri] = struct{}{}
				audioVariants = append(audioVariants, AudioVariant{
					URI:     uri,
					Line:    line,
					Default: strings.Contains(line, "DEFAULT=YES"),
				})
			}
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			bandwidth, err := readIntAttribute(line, "BANDWIDTH")
			if err != nil {
				return MasterPlaylist{}, err
			}
			currentVideo = VideoVariant{
				StreamLine: line,
				Bandwidth:  bandwidth,
			}
			expectVideoURI = true
			continue
		}

		if expectVideoURI && !strings.HasPrefix(line, "#") {
			if _, exists := videoSeen[line]; !exists {
				videoSeen[line] = struct{}{}
				currentVideo.URI = line
				videoVariants = append(videoVariants, currentVideo)
			}
			expectVideoURI = false
		}
	}

	if err := scanner.Err(); err != nil {
		return MasterPlaylist{}, fmt.Errorf("failed to read master playlist: %w", err)
	}
	if len(audioVariants) == 0 {
		return MasterPlaylist{}, fmt.Errorf("audio playlist URI was not found in master playlist")
	}
	if len(videoVariants) == 0 {
		return MasterPlaylist{}, fmt.Errorf("video playlist URI was not found in master playlist")
	}

	return MasterPlaylist{
		AudioVariants: audioVariants,
		VideoVariants: videoVariants,
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

func BuildSingleVariantMaster(master MasterPlaylist) ([]byte, error) {
	audio, err := master.SelectAudio()
	if err != nil {
		return nil, err
	}
	video, err := master.SelectVideo()
	if err != nil {
		return nil, err
	}

	return []byte(strings.Join([]string{
		"#EXTM3U",
		audio.Line,
		video.StreamLine,
		video.URI,
		"",
	}, "\n")), nil
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
