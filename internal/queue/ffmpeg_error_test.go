package queue

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFFmpegErrorTail(t *testing.T) {
	t.Run("empty output", func(t *testing.T) {
		if got := ffmpegErrorTail(nil); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
		if got := ffmpegErrorTail([]byte("\n \n")); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("short output is kept whole", func(t *testing.T) {
		out := "in.mkv: Invalid data found when processing input\n"
		if got, want := ffmpegErrorTail([]byte(out)), strings.TrimSpace(out); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("long output keeps the last lines", func(t *testing.T) {
		var b strings.Builder
		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		got := ffmpegErrorTail([]byte(b.String()))
		lines := strings.Split(got, "\n")
		if len(lines) != ffmpegErrorLines+1 {
			t.Fatalf("got %d lines, want %d plus a marker:\n%s", len(lines), ffmpegErrorLines, got)
		}
		if lines[0] != "[25 earlier lines omitted]" {
			t.Errorf("marker = %q", lines[0])
		}
		if lines[1] != "line 26" || lines[len(lines)-1] != "line 40" {
			t.Errorf("kept %q .. %q, want line 26 .. line 40", lines[1], lines[len(lines)-1])
		}
	})

	t.Run("long lines are capped in bytes on a rune boundary", func(t *testing.T) {
		out := strings.Repeat("é", 3000) + "\nConversion failed!\n"
		got := ffmpegErrorTail([]byte(out))
		if !strings.HasPrefix(got, "[earlier output omitted]\n") {
			t.Fatalf("missing marker: %q", got[:40])
		}
		body := strings.TrimPrefix(got, "[earlier output omitted]\n")
		if len(body) > ffmpegErrorBytes {
			t.Errorf("kept %d bytes, cap is %d", len(body), ffmpegErrorBytes)
		}
		if !utf8.ValidString(body) {
			t.Errorf("kept text is not valid UTF-8")
		}
		if !strings.HasSuffix(body, "Conversion failed!") {
			t.Errorf("lost the final line: %q", body[len(body)-40:])
		}
	})
}
