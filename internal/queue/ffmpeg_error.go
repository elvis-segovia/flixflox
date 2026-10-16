package queue

import (
	"bytes"
	"fmt"
	"unicode/utf8"
)

const (
	// ffmpegErrorLines is how much of ffmpeg's output a failed job keeps.
	// ffmpeg prints the reason it gave up last, so the tail is the part that
	// explains the failure.
	ffmpegErrorLines = 15
	// ffmpegErrorBytes caps the kept tail for the case of a few very long
	// lines, such as a stream dump or a repeated decoder warning.
	ffmpegErrorBytes = 2048
)

// ffmpegErrorTail returns the last lines of ffmpeg's combined output, for the
// error stored on a failed job. Job.Error is served by the queue info
// endpoint, so it should carry the reason for the failure, not the whole log;
// the full output is logged server-side.
func ffmpegErrorTail(out []byte) string {
	out = bytes.TrimRight(out, " \t\r\n")
	if len(out) == 0 {
		return ""
	}

	lines := bytes.Split(out, []byte("\n"))
	omitted := 0
	if len(lines) > ffmpegErrorLines {
		omitted = len(lines) - ffmpegErrorLines
		lines = lines[omitted:]
	}
	tail := bytes.Join(lines, []byte("\n"))

	if len(tail) > ffmpegErrorBytes {
		cut := len(tail) - ffmpegErrorBytes
		// Do not start the kept text in the middle of a UTF-8 sequence.
		for cut < len(tail) && !utf8.RuneStart(tail[cut]) {
			cut++
		}
		tail = tail[cut:]
		if omitted == 0 {
			omitted = -1
		}
	}

	switch {
	case omitted > 0:
		return fmt.Sprintf("[%d earlier lines omitted]\n%s", omitted, tail)
	case omitted < 0:
		return fmt.Sprintf("[earlier output omitted]\n%s", tail)
	default:
		return string(tail)
	}
}
