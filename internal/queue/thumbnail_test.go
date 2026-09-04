package queue

import (
	"testing"
	"time"
)

func TestThumbnailSeekOffset(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     time.Duration
	}{
		{
			name:     "unknown duration falls back to 5s",
			duration: 0,
			want:     5 * time.Second,
		},
		{
			name:     "negative duration falls back to 5s",
			duration: -time.Second,
			want:     5 * time.Second,
		},
		{
			name:     "100 minute film seeks to 10 minutes",
			duration: 100 * time.Minute,
			want:     10 * time.Minute,
		},
		{
			name:     "90 second clip seeks to 9 seconds",
			duration: 90 * time.Second,
			want:     9 * time.Second,
		},
		{
			name:     "10 second clip seeks to 1 second",
			duration: 10 * time.Second,
			want:     time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := thumbnailSeekOffset(tt.duration)
			if got != tt.want {
				t.Fatalf("thumbnailSeekOffset(%v) = %v, want %v", tt.duration, got, tt.want)
			}
		})
	}
}

func TestFormatSeekTimestamp(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "00:00:00.000"},
		{5 * time.Second, "00:00:05.000"},
		{90 * time.Second, "00:01:30.000"},
		{10*time.Minute + 250*time.Millisecond, "00:10:00.250"},
		{time.Hour + 2*time.Minute + 3*time.Second + 7*time.Millisecond, "01:02:03.007"},
	}

	for _, tt := range tests {
		got := formatSeekTimestamp(tt.d)
		if got != tt.want {
			t.Errorf("formatSeekTimestamp(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestThumbnailFFmpegArgs_ssBeforeInput(t *testing.T) {
	args := thumbnailFFmpegArgs("/in/movie.mkv", "/out/thumbnail.jpg", 100*time.Minute)

	// Expect: ffmpeg -y -ss <offset> -i <input> -vframes 1 <output>
	want := []string{
		"-y",
		"-ss", "00:10:00.000",
		"-i", "/in/movie.mkv",
		"-vframes", "1",
		"/out/thumbnail.jpg",
	}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d\nargs: %#v", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q\nfull: %#v", i, args[i], want[i], args)
		}
	}

	// -ss must appear before -i (input seek).
	ssIdx, iIdx := -1, -1
	for i, a := range args {
		switch a {
		case "-ss":
			ssIdx = i
		case "-i":
			iIdx = i
		}
	}
	if ssIdx < 0 || iIdx < 0 || ssIdx >= iIdx {
		t.Fatalf("-ss must come before -i; ss=%d i=%d args=%v", ssIdx, iIdx, args)
	}
}

func TestThumbnailFFmpegArgs_fallbackWhenDurationUnknown(t *testing.T) {
	args := thumbnailFFmpegArgs("in.mp4", "thumb.jpg", 0)
	if got, want := args[2], "00:00:05.000"; got != want {
		t.Fatalf("fallback -ss = %q, want %q; args=%v", got, want, args)
	}
	if args[1] != "-ss" || args[3] != "-i" {
		t.Fatalf("expected -ss before -i, got %v", args)
	}
}
