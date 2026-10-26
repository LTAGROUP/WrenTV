package match

import (
	"testing"

	"github.com/LTAGROUP/wrentv/internal/sports"
)

func TestReportedQuality(t *testing.T) {
	tests := []struct {
		label     string
		height    int
		bitrate   int64
		wantEmpty bool
	}{
		{label: "ESPN 4K", height: 2160},
		{label: "Sky Sports FHD", height: 1080},
		{label: "Feed 1080p 60fps", height: 1080},
		{label: "Channel 720p 8 Mbps", height: 720, bitrate: 8000000},
		{label: "2160p 25Mbps", height: 2160, bitrate: 25000000},
		// Conflicting labels retain the lower resolution.
		{label: "4K backup 1080p", height: 1080},
		// No explicit quality: nil, never a guess from an HD-ish name.
		{label: "Sports HD", wantEmpty: true},
		{label: "News", wantEmpty: true},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got := ReportedQuality(tt.label)
			if tt.wantEmpty {
				if got != nil {
					t.Fatalf("ReportedQuality(%q) = %+v, want nil", tt.label, got)
				}
				return
			}
			if got == nil || got.ResolutionHeight != tt.height || got.BitrateBps != tt.bitrate || got.Origin != "label" {
				t.Fatalf("ReportedQuality(%q) = %+v, want height %d bitrate %d", tt.label, got, tt.height, tt.bitrate)
			}
		})
	}
}

func TestCompareQuality(t *testing.T) {
	a := ReportedQuality("2160p 25Mbps")
	b := ReportedQuality("1080p 10Mbps")
	c := ReportedQuality("1080p 20Mbps")
	if CompareQuality(a, b) >= 0 {
		t.Fatal("4K should rank before 1080p")
	}
	if CompareQuality(c, b) >= 0 {
		t.Fatal("higher bitrate should rank first at equal height")
	}
	if CompareQuality(nil, b) <= 0 {
		t.Fatal("known quality should rank before unknown")
	}
	if CompareQuality(nil, nil) != 0 {
		t.Fatal("two unknown qualities should compare equal")
	}
}

func TestSortStreamMatchesUsesQualityAfterConfidence(t *testing.T) {
	matches := []sports.SportsStreamMatch{
		{ChannelID: "hd", ChannelName: "Sports HD", Confidence: .9},
		{ChannelID: "4k", ChannelName: "Sports 4K", Confidence: .9},
		{ChannelID: "sd", ChannelName: "Sports SD", Confidence: .9},
	}
	sortStreamMatches(matches)
	if matches[0].ChannelID != "4k" {
		t.Fatalf("order = %s,%s,%s, want 4k first", matches[0].ChannelID, matches[1].ChannelID, matches[2].ChannelID)
	}
}
