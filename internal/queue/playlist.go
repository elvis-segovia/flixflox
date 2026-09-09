package queue

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// PlaylistDurationSeconds sums the #EXTINF values of a finished HLS media
// playlist. For a VOD playlist that is the exact title duration, and it needs
// no access to the source file — which the conversion deletes.
//
// It refuses playlists that cannot account for the whole title: one still being
// written (no #EXT-X-ENDLIST) or a sliding window that has already dropped
// segments (HLS_LIST_SIZE > 0, visible as a non-zero media sequence).
func PlaylistDurationSeconds(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var (
		total         float64
		segments      int
		mediaSequence int
		ended         bool
	)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			// #EXTINF:<seconds>,[<title>]
			value := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.Index(value, ","); i >= 0 {
				value = value[:i]
			}
			secs, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || secs < 0 || math.IsInf(secs, 0) || math.IsNaN(secs) {
				return 0, fmt.Errorf("%s: unparseable %q", path, line)
			}
			total += secs
			segments++
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			mediaSequence, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:")))
		case line == "#EXT-X-ENDLIST":
			ended = true
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}

	switch {
	case segments == 0:
		return 0, fmt.Errorf("%s: no #EXTINF entries", path)
	case !ended:
		return 0, fmt.Errorf("%s: no #EXT-X-ENDLIST, playlist is incomplete", path)
	case mediaSequence > 0:
		return 0, fmt.Errorf("%s: sliding-window playlist (media sequence %d), earlier segments are gone", path, mediaSequence)
	}

	return total, nil
}
