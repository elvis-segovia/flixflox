package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type Episode struct {
	EpisodeNumber           int     `json:"episode_number" bson:"episode_number"`
	Title                   string  `json:"title,omitempty" bson:"title,omitempty"`
	DurationSeconds         float64 `json:"duration_seconds,omitempty" bson:"duration_seconds,omitempty"`
	FilePath                string  `json:"file_path,omitempty" bson:"file_path,omitempty"`
	ThumbailPath            string  `json:"thumbail_path,omitempty" bson:"thumbail_path,omitempty"`
	Status                  string  `json:"status,omitempty" bson:"status,omitempty"`
	SkipIntroDisplayMessage string  `json:"skip_intro_display_message,omitempty" bson:"skip_intro_display_message,omitempty"`
	IntroStartTime          string  `json:"intro_start_time,omitempty" bson:"intro_start_time,omitempty"`
	IntroEndTime            string  `json:"intro_end_time,omitempty" bson:"intro_end_time,omitempty"`
	NextEpisodeTime         string  `json:"next_episode_time,omitempty" bson:"next_episode_time,omitempty"`
}

type Season struct {
	SeasonNumber int       `json:"season_number" bson:"season_number"`
	Episodes     []Episode `json:"episodes" bson:"episodes"`
}

type CatalogItem struct {
	ID                      bson.ObjectID `json:"id" bson:"_id,omitempty"`
	UUID                    string        `json:"uuid" bson:"uuid"`
	Title                   string        `json:"title" bson:"title"`
	Type                    string        `json:"type" bson:"type"` // "movie" or "tvshow"
	ReleaseYear             int           `json:"release_year" bson:"release_year"`
	Genre                   []string      `json:"genre" bson:"genre"`
	Rating                  float64       `json:"rating" bson:"rating"`
	Description             string        `json:"description,omitempty" bson:"description,omitempty"`
	Cast                    []string      `json:"cast,omitempty" bson:"cast,omitempty"`
	DurationSeconds         float64       `json:"duration_seconds,omitempty" bson:"duration_seconds,omitempty"`
	FilePath                string        `json:"file_path,omitempty" bson:"file_path,omitempty"`
	BGImage                 string        `json:"bg_image,omitempty" bson:"bg_image,omitempty"`
	SkipIntroDisplayMessage string        `json:"skip_intro_display_message,omitempty" bson:"skip_intro_display_message,omitempty"`
	IntroStartTime          string        `json:"intro_start_time,omitempty" bson:"intro_start_time,omitempty"`
	IntroEndTime            string        `json:"intro_end_time,omitempty" bson:"intro_end_time,omitempty"`
	Status                  string        `json:"status" bson:"status"`
	Seasons                 []Season      `json:"seasons,omitempty" bson:"seasons,omitempty"`
	CreatedAt               time.Time     `json:"created_at" bson:"created_at"`
	UpdatedAt               time.Time     `json:"updated_at" bson:"updated_at"`
}

type Pagination struct {
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"total_pages"`
}
