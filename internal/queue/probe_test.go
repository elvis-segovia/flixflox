package queue

import (
	"testing"
	"time"
)

func TestParseProbeOutput(t *testing.T) {
	out := []byte(`{
	  "streams": [
	    {"index":0,"codec_name":"h264","codec_type":"video","pix_fmt":"yuv420p","duration":"2712.000000"},
	    {"index":1,"codec_name":"aac","codec_type":"audio","duration":"2712.000000"},
	    {"index":2,"codec_name":"subrip","codec_type":"subtitle"}
	  ],
	  "format": {"duration":"2712.041000","format_name":"mov,mp4,m4a"}
	}`)

	info := parseProbeOutput(out)
	if info.VideoCodec != "h264" {
		t.Errorf("VideoCodec = %q, want h264", info.VideoCodec)
	}
	if info.AudioCodec != "aac" {
		t.Errorf("AudioCodec = %q, want aac", info.AudioCodec)
	}
	if info.PixFmt != "yuv420p" {
		t.Errorf("PixFmt = %q, want yuv420p", info.PixFmt)
	}
	if want := 2712041 * time.Millisecond; info.Duration != want {
		t.Errorf("Duration = %v, want %v", info.Duration, want)
	}
}

func TestParseProbeOutput_FirstStreamOfEachKindWins(t *testing.T) {
	// ffmpeg maps 0:v:0 / 0:a:0, so a second video stream (cover art, say)
	// must not override the codec decision.
	out := []byte(`{"streams":[
	  {"codec_name":"hevc","codec_type":"video","pix_fmt":"yuv420p10le"},
	  {"codec_name":"mjpeg","codec_type":"video","pix_fmt":"yuvj420p"},
	  {"codec_name":"mp3","codec_type":"audio"},
	  {"codec_name":"aac","codec_type":"audio"}
	],"format":{"duration":"60.0"}}`)

	info := parseProbeOutput(out)
	if info.VideoCodec != "hevc" || info.PixFmt != "yuv420p10le" {
		t.Errorf("video = %q/%q, want hevc/yuv420p10le", info.VideoCodec, info.PixFmt)
	}
	if info.AudioCodec != "mp3" {
		t.Errorf("AudioCodec = %q, want mp3", info.AudioCodec)
	}
}

func TestParseProbeOutput_FallsBackToStreamDuration(t *testing.T) {
	// Matroska commonly reports no format.duration.
	out := []byte(`{"streams":[
	  {"codec_name":"h264","codec_type":"video","pix_fmt":"yuv420p","duration":"N/A"},
	  {"codec_name":"aac","codec_type":"audio","duration":"1234.500000"}
	],"format":{"duration":"N/A"}}`)

	info := parseProbeOutput(out)
	if want := 1234500 * time.Millisecond; info.Duration != want {
		t.Errorf("Duration = %v, want %v", info.Duration, want)
	}
}

func TestParseProbeOutput_Unusable(t *testing.T) {
	tests := []struct {
		name string
		out  string
	}{
		{"not json", "ffprobe: command failed"},
		{"empty document", `{}`},
		{"no duration anywhere", `{"streams":[{"codec_name":"h264","codec_type":"video"}],"format":{}}`},
		{"negative duration", `{"streams":[],"format":{"duration":"-5"}}`},
		{"non-finite duration", `{"streams":[],"format":{"duration":"inf"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseProbeOutput([]byte(tt.out)).Duration; got != 0 {
				t.Fatalf("Duration = %v, want 0", got)
			}
		})
	}
}
