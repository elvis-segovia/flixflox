package queue

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePlaylist(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out.m3u8")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write playlist: %v", err)
	}
	return path
}

const vodPlaylist = `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-TARGETDURATION:10
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-PLAYLIST-TYPE:VOD
#EXT-X-MAP:URI="init.mp4"
#EXTINF:10.000000,
movie_000.m4s
#EXTINF:10.000000,
movie_001.m4s
#EXTINF:4.500000,
movie_002.m4s
#EXT-X-ENDLIST
`

func TestPlaylistDurationSeconds(t *testing.T) {
	got, err := PlaylistDurationSeconds(writePlaylist(t, vodPlaylist))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := 24.5; got != want {
		t.Fatalf("duration = %v, want %v", got, want)
	}
}

func TestPlaylistDurationSeconds_EXTINFWithTitle(t *testing.T) {
	body := "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:6.006,segment title\na.ts\n#EXT-X-ENDLIST\n"
	got, err := PlaylistDurationSeconds(writePlaylist(t, body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := 6.006; got != want {
		t.Fatalf("duration = %v, want %v", got, want)
	}
}

func TestPlaylistDurationSeconds_Rejects(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "still being written",
			body:    "#EXTM3U\n#EXTINF:10.0,\na.ts\n",
			wantErr: "EXT-X-ENDLIST",
		},
		{
			name:    "sliding window dropped segments",
			body:    "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:12\n#EXTINF:10.0,\na.ts\n#EXT-X-ENDLIST\n",
			wantErr: "sliding-window",
		},
		{
			name:    "no segments",
			body:    "#EXTM3U\n#EXT-X-ENDLIST\n",
			wantErr: "no #EXTINF",
		},
		{
			name:    "garbage EXTINF",
			body:    "#EXTM3U\n#EXTINF:abc,\na.ts\n#EXT-X-ENDLIST\n",
			wantErr: "unparseable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PlaylistDurationSeconds(writePlaylist(t, tt.body))
			if err == nil {
				t.Fatalf("expected error, got duration %v", got)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to mention %q", err, tt.wantErr)
			}
			if got != 0 {
				t.Fatalf("duration = %v on error, want 0", got)
			}
		})
	}
}

func TestPlaylistDurationSeconds_MissingFile(t *testing.T) {
	if _, err := PlaylistDurationSeconds(filepath.Join(t.TempDir(), "nope.m3u8")); err == nil {
		t.Fatal("expected an error for a missing playlist")
	}
}
