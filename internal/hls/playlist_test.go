package hls

import (
	"slices"
	"testing"
)

func TestParseMasterKeepsAllReferencesAndSelectsSingleVariant(t *testing.T) {
	t.Parallel()

	body := []byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="low",DEFAULT=NO,URI="audio-low.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="high",DEFAULT=YES,URI="audio-high.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=1500000,RESOLUTION=1280x720,AUDIO="audio"
video-720.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,AUDIO="audio"
video-1080.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360,AUDIO="audio"
video-360.m3u8
`)

	master, err := ParseMaster(body)
	if err != nil {
		t.Fatal(err)
	}

	allReferences := master.References()
	expectedAllReferences := []string{
		"audio-low.m3u8",
		"audio-high.m3u8",
		"video-720.m3u8",
		"video-1080.m3u8",
		"video-360.m3u8",
	}
	if !slices.Equal(allReferences, expectedAllReferences) {
		t.Fatalf("expected all references %#v, got %#v", expectedAllReferences, allReferences)
	}

	singleReferences, err := master.SingleVariantReferences()
	if err != nil {
		t.Fatal(err)
	}
	expectedSingleReferences := []string{"audio-high.m3u8", "video-1080.m3u8"}
	if !slices.Equal(singleReferences, expectedSingleReferences) {
		t.Fatalf("expected single references %#v, got %#v", expectedSingleReferences, singleReferences)
	}

	localMaster, err := BuildSingleVariantMaster(master)
	if err != nil {
		t.Fatal(err)
	}
	expectedLocalMaster := "#EXTM3U\n" +
		"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=\"high\",DEFAULT=YES,URI=\"audio-high.m3u8\"\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,AUDIO=\"audio\"\n" +
		"video-1080.m3u8\n"
	if string(localMaster) != expectedLocalMaster {
		t.Fatalf("expected local master %q, got %q", expectedLocalMaster, string(localMaster))
	}
}

func TestSingleVariantReferencesRejectsAmbiguousAudio(t *testing.T) {
	t.Parallel()

	body := []byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="low",DEFAULT=NO,URI="audio-low.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="high",DEFAULT=NO,URI="audio-high.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080,AUDIO="audio"
video-1080.m3u8
`)

	master, err := ParseMaster(body)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := master.SingleVariantReferences(); err == nil {
		t.Fatal("expected ambiguous audio error")
	}
}
