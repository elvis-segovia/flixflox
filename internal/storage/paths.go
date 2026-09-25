// Package storage owns the on-disk layout of the upload folder. Handlers and
// the conversion queue both build paths into it, so the rules live here once:
// every path the package returns is relative to the upload root, and only the
// caller that touches the filesystem joins it with cfg.UploadFolder.
package storage

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	MoviesDir  = "movies"
	TVShowsDir = "tv-shows"
)

const ThumbnailName = "thumbnail.jpg"

type Layout struct {
	Dir        string
	TitleDir   string
	OutputName string
}

func TypeDir(contentType string) string {
	if contentType == "movie" {
		return MoviesDir
	}
	return TVShowsDir
}

func TitleDir(contentType, title string) string {
	return filepath.Join(TypeDir(contentType), SanitizeFilename(title))
}

func MovieLayout(title string) Layout {
	dir := TitleDir("movie", title)
	return Layout{
		Dir:        dir,
		TitleDir:   dir,
		OutputName: SanitizeFilename(title),
	}
}

func EpisodeLayout(title string, season, episode int) Layout {
	titleDir := TitleDir("tvshow", title)
	return Layout{
		Dir:        filepath.Join(titleDir, fmt.Sprintf("S%02d", season), fmt.Sprintf("E%02d", episode)),
		TitleDir:   titleDir,
		OutputName: fmt.Sprintf("%s_S%02dE%02d", SanitizeFilename(title), season, episode),
	}
}

func PlaylistFile(outputName string) string {
	return outputName + ".m3u8"
}

func SegmentPattern(outputName, segmentExt string) string {
	return fmt.Sprintf("%s_%%03d.%s", outputName, segmentExt)
}

func Resolve(uploadFolder string, elem ...string) string {
	return filepath.Join(append([]string{uploadFolder}, elem...)...)
}

func SanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		" ", "_", "/", "_", "\\", "_", ":", "_",
		"*", "_", "?", "_", "\"", "_", "<", "_",
		">", "_", "|", "_",
	)
	return strings.ToLower(replacer.Replace(name))
}
