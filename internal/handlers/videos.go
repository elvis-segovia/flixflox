package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elvis/flixflox/internal/config"
	"github.com/elvis/flixflox/internal/database"
	"github.com/elvis/flixflox/internal/middleware"
	"github.com/elvis/flixflox/internal/models"
	"github.com/elvis/flixflox/internal/queue"
	"github.com/elvis/flixflox/internal/storage"
	"github.com/elvis/flixflox/internal/utils"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var allowedExtensions = map[string]bool{
	".mp4": true, ".avi": true, ".flv": true, ".mkv": true,
	".mov": true, ".wmv": true, ".webm": true,
}

type episodeMetadata struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Title   string `json:"title"`
}

func parseEpisodeMetadata(r *http.Request) episodeMetadata {
	var metadata episodeMetadata
	if metaStr := r.FormValue("metadata"); metaStr != "" {
		json.Unmarshal([]byte(metaStr), &metadata)
	}
	if metadata.Season == 0 {
		metadata.Season = 1
	}
	if metadata.Episode == 0 {
		metadata.Episode = 1
	}
	return metadata
}

func videoExtAllowed(filename string) bool {
	return allowedExtensions[strings.ToLower(filepath.Ext(filename))]
}

func saveUploadedFile(path string, src io.Reader) error {
	dst, err := os.Create(path)
	if err != nil {
		return err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Close()
}

func RegisterVideoRoutes(r chi.Router, client *mongo.Client, cfg *config.Config, q *queue.ConversionQueue) {
	// Public routes
	r.Get("/v1/api/videos", handleListVideos(client))
	r.Get("/v1/api/videos/{vtype}/list", handleListVideosByType(client))
	r.Get("/v1/api/videos/{id}/details", handleGetVideoDetails(client))
	r.Get("/v1/api/videos/{id}/season/{season}", handleGetVideoBySeason(client))
	r.Get("/v1/api/videos/image/*", handleBgImage(cfg))
	r.Get("/v1/api/videos/stream/*", handleStream(cfg))
	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.JWTAuth(cfg.JWTSecret, client))
		r.Post("/v1/api/videos", handleCreateVideo(client))
		r.Post("/v1/api/videos/upload", handleUploadVideo(client, cfg, q))
		r.Put("/v1/api/videos/{id}/new-episode", handleAddEpisode(client, cfg, q))
		r.Put("/v1/api/videos/{uuid}/season/{season}/episode/{episode}", handleUpdateVideoBySeasonAndEpisode(client))
		r.Get("/v1/api/videos/queue/info", handleQueueInfo(q))
		r.Post("/v1/api/videos/queue/start", handleQueueStart(q))
		r.Post("/v1/api/videos/queue/cleanup", handleQueueCleanup(q))
	})
}

func handleListVideos(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		cursor, err := coll.Find(ctx, bson.M{})
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to fetch videos")
			return
		}
		defer cursor.Close(ctx)

		var items []models.CatalogItem
		if err := cursor.All(ctx, &items); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to decode videos")
			return
		}

		utils.Success(w, http.StatusOK, items)
	}
}

func handleListVideosByType(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vtype := chi.URLParam(r, "vtype")
		if vtype != "movie" && vtype != "tvshow" {
			utils.Error(w, http.StatusBadRequest, "Type must be 'movie' or 'tvshow'")
			return
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage < 1 || perPage > 100 {
			perPage = 20
		}

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		filter := bson.M{"type": vtype}
		total, err := coll.CountDocuments(ctx, filter)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to count videos")
			return
		}

		skip := int64((page - 1) * perPage)
		limit := int64(perPage)
		cursor, err := coll.Find(ctx, filter, options.Find().SetSkip(skip).SetLimit(limit).SetSort(bson.D{{Key: "created_at", Value: -1}}))
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to fetch videos")
			return
		}
		defer cursor.Close(ctx)

		var items []models.CatalogItem
		if err := cursor.All(ctx, &items); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to decode videos")
			return
		}

		totalPages := (total + int64(perPage) - 1) / int64(perPage)

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"data":   items,
			"pagination": models.Pagination{
				Page:       page,
				PerPage:    perPage,
				Total:      total,
				TotalPages: totalPages,
			},
		})
	}
}

func handleGetVideoDetails(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentID := chi.URLParam(r, "id")

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var item models.CatalogItem
		err := coll.FindOne(ctx, bson.M{"uuid": contentID}).Decode(&item)
		if err != nil {
			utils.Error(w, http.StatusNotFound, "Content not found")
			return
		}

		utils.Success(w, http.StatusOK, item)
	}
}

func handleGetVideoBySeason(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentID := chi.URLParam(r, "id")
		seasonNum, err := strconv.Atoi(chi.URLParam(r, "season"))
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid season number")
			return
		}

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var item models.CatalogItem
		if err := coll.FindOne(ctx, bson.M{"uuid": contentID}).Decode(&item); err != nil {
			utils.Error(w, http.StatusNotFound, "Content not found")
			return
		}

		for _, s := range item.Seasons {
			if s.SeasonNumber == seasonNum {
				utils.Success(w, http.StatusOK, s.Episodes)
				return
			}
		}

		utils.Error(w, http.StatusNotFound, "Season not found")
	}
}

func handleUpdateVideoBySeasonAndEpisode(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		contentUUID := chi.URLParam(r, "uuid")
		seasonNumber, err := strconv.Atoi(chi.URLParam(r, "season"))
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid season")
			return
		}

		episodeNumber, err := strconv.Atoi(chi.URLParam(r, "episode"))
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid episode")
			return
		}

		var updates models.Episode
		if err := utils.DecodeBody(r, &updates); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		set := bson.M{
			"updated_at": time.Now(),
		}

		if updates.Title != "" {
			set["seasons.$[s].episodes.$[e].title"] = updates.Title
		}

		if updates.SkipIntroDisplayMessage != "" {
			set["seasons.$[s].episodes.$[e].skip_intro_display_message"] = updates.SkipIntroDisplayMessage
		}

		if updates.IntroStartTime != "" {
			set["seasons.$[s].episodes.$[e].intro_start_time"] = updates.IntroStartTime
		}

		if updates.IntroEndTime != "" {
			set["seasons.$[s].episodes.$[e].intro_end_time"] = updates.IntroEndTime
		}

		if updates.NextEpisodeTime != "" {
			set["seasons.$[s].episodes.$[e].next_episode_time"] = updates.NextEpisodeTime
		}

		coll := database.Collection(client, "catalog")

		result, err := coll.UpdateOne(
			ctx,
			bson.M{"uuid": contentUUID},
			bson.M{"$set": set},
			options.UpdateOne().SetArrayFilters([]any{
				bson.M{"s.season_number": seasonNumber},
				bson.M{"e.episode_number": episodeNumber},
			}),
		)

		if err != nil {
			utils.Error(w, http.StatusInternalServerError, err.Error())
			return
		}

		if result.MatchedCount == 0 {
			utils.Error(w, http.StatusNotFound, fmt.Sprintf("Content %s season %v episode %v not found", contentUUID, seasonNumber, episodeNumber))
			return
		}

		utils.JSON(w, http.StatusOK, map[string]string{
			"message": "Episode updated successfully",
		})
	}
}

func handleStream(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		root, err := os.OpenRoot(cfg.UploadFolder)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, fmt.Sprintf("Invalid file path %v", err))
			return
		}

		relPath := filepath.Clean(chi.URLParam(r, "*"))
		if relPath == "." || filepath.IsAbs(relPath) {
			utils.Error(w, http.StatusBadRequest, "Invalid file path")
			return
		}

		ext := strings.ToLower(filepath.Ext(relPath))

		switch ext {
		case ".m3u8":
			w.Header().Set("Content-Type", "application/x-mpegURL")
		case ".ts":
			w.Header().Set("Content-Type", "video/MP2T")
		case ".m4s":
			w.Header().Set("Content-Type", "video/iso.segment")
		case ".mp4":
			w.Header().Set("Content-Type", "video/mp4")
		}

		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, root.FS(), relPath)
	}
}

func handleBgImage(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		root, err := os.OpenRoot(cfg.UploadFolder)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, fmt.Sprintf("Invalid file path %s", cfg.UploadFolder))
			return
		}

		relPath := filepath.Clean(chi.URLParam(r, "*"))
		if relPath == "." || filepath.IsAbs(relPath) {
			fmt.Printf("relPath: %s", relPath)
			utils.Error(w, http.StatusBadRequest, "Invalid file path")
			return
		}

		ext := strings.ToLower(filepath.Ext(relPath))
		switch ext {
		case ".jpg":
			w.Header().Set("Content-Type", "image/jpeg")
		case ".png":
			w.Header().Set("Content-Type", "image/png")
		case ".jpeg":
			w.Header().Set("Content-Type", "image/jpeg")
		}

		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, root.FS(), relPath)
	}
}

func handleCreateVideo(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var item models.CatalogItem
		if err := utils.DecodeBody(r, &item); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if item.Title == "" || item.Type == "" {
			utils.Error(w, http.StatusBadRequest, "Title and type are required")
			return
		}

		now := time.Now()
		item.UUID = uuid.New().String()
		item.Status = "In-Progress"
		item.CreatedAt = now
		item.UpdatedAt = now

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.InsertOne(ctx, item)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to create content")
			return
		}

		utils.Success(w, http.StatusCreated, map[string]any{
			"status": "success",
			"id":     result.InsertedID,
		})
	}
}

func handleUploadVideo(client *mongo.Client, cfg *config.Config, q *queue.ConversionQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(cfg.MaxFileSize); err != nil {
			utils.Error(w, http.StatusBadRequest, "File too large or invalid form data")
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "File is required")
			return
		}
		defer file.Close()

		const maxUploadSize = 2 * 1024 * 1024 // 5MB
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			http.Error(w, "File size exceeds the 2MB limit", http.StatusBadRequest)
			return
		}

		hasImage := true
		image, poster, err := r.FormFile("bg_image")
		if err != nil {
			if errors.Is(err, http.ErrMissingFile) {
				hasImage = false
			} else {
				utils.Error(w, http.StatusBadRequest, "Invalid bg_image upload")
				return
			}
		}

		if hasImage {
			defer image.Close()

			buff := make([]byte, 512)
			if _, err := image.Read(buff); err != nil {
				http.Error(w, "Failed to read file headers", http.StatusInternalServerError)
				return
			}

			if _, err := image.Seek(0, io.SeekStart); err != nil {
				http.Error(w, "Failed to reset file pointer", http.StatusInternalServerError)
				return
			}

			fileType := http.DetectContentType(buff)
			if fileType != "image/jpeg" && fileType != "image/png" && fileType != "image/jpg" {
				http.Error(w, "Unsupported file format. Please upload JPEG, PNG, or JPG.", http.StatusBadRequest)
				return
			}
		}

		if !videoExtAllowed(header.Filename) {
			utils.Error(w, http.StatusBadRequest, fmt.Sprintf("File type %s not allowed", filepath.Ext(header.Filename)))
			return
		}

		contentType := r.FormValue("type")
		if contentType != "movie" && contentType != "tvshow" {
			utils.Error(w, http.StatusBadRequest, "Type must be 'movie' or 'tvshow'")
			return
		}

		var values map[string]any
		if err := json.Unmarshal([]byte(r.FormValue("values")), &values); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid values JSON")
			return
		}

		title, _ := values["title"].(string)
		if title == "" {
			utils.Error(w, http.StatusBadRequest, "Title is required in values")
			return
		}

		contentUUID := uuid.New().String()
		now := time.Now()

		var showDetails struct {
			Title                   string `json:"title"`
			SkipIntroDisplayMessage string `json:"skip_intro_display_message"`
			IntroStartTime          string `json:"intro_start_time"`
			IntroEndTime            string `json:"intro_end_time,omitempty"`
			NextEpisodeTime         string `json:"next_episode_time,omitempty"`
		}

		var layout storage.Layout
		var season, episode int
		if contentType == "movie" {
			layout = storage.MovieLayout(title)
		} else {
			metadata := parseEpisodeMetadata(r)
			season, episode = metadata.Season, metadata.Episode
			layout = storage.EpisodeLayout(title, season, episode)
		}

		item := models.CatalogItem{
			UUID:      contentUUID,
			Title:     title,
			Type:      contentType,
			Status:    "In-Progress",
			CreatedAt: now,
			UpdatedAt: now,
		}

		if hasImage {
			item.BGImage = filepath.Join(layout.TitleDir, filepath.Base(poster.Filename))
		}

		if ry, ok := values["release_year"].(float64); ok {
			item.ReleaseYear = int(ry)
		}
		if rating, ok := values["rating"].(float64); ok {
			item.Rating = rating
		}
		if desc, ok := values["description"].(string); ok {
			item.Description = desc
		}
		if genres, ok := values["genre"].([]any); ok {
			for _, g := range genres {
				if s, ok := g.(string); ok {
					item.Genre = append(item.Genre, s)
				}
			}
		}
		if cast, ok := values["cast"].([]any); ok {
			for _, c := range cast {
				if s, ok := c.(string); ok {
					item.Cast = append(item.Cast, s)
				}
			}
		}

		if details, ok := values["show_details"].([]any); ok && len(details) > 0 {
			if m, ok := details[0].(map[string]any); ok {
				b, err := json.Marshal(m)
				if err != nil {
					utils.Error(w, http.StatusInternalServerError, "Error getting show details")
					return
				}

				if err := json.Unmarshal(b, &showDetails); err != nil {
					utils.Error(w, http.StatusInternalServerError, "Error unmarshal show details")
					return
				}
			}
		}

		if contentType != "movie" {
			item.Seasons = []models.Season{{
				SeasonNumber: season,
				Episodes: []models.Episode{{
					EpisodeNumber:           episode,
					Title:                   showDetails.Title,
					Status:                  "In-Progress",
					SkipIntroDisplayMessage: showDetails.SkipIntroDisplayMessage,
					IntroStartTime:          showDetails.IntroStartTime,
					IntroEndTime:            showDetails.IntroEndTime,
					NextEpisodeTime:         showDetails.NextEpisodeTime,
				}},
			}}
		}

		fullPath := storage.Resolve(cfg.UploadFolder, layout.Dir)

		if err := os.MkdirAll(fullPath, 0755); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to create upload directory")
			return
		}

		if hasImage {
			posterDir := storage.Resolve(cfg.UploadFolder, layout.TitleDir)
			if err := os.MkdirAll(posterDir, 0755); err != nil {
				utils.Error(w, http.StatusInternalServerError, "Failed to create upload directory")
				return
			}

			if err := saveUploadedFile(filepath.Join(posterDir, filepath.Base(poster.Filename)), image); err != nil {
				utils.Error(w, http.StatusInternalServerError, "Unable to save image locally")
				return
			}
		}

		inputPath := filepath.Join(fullPath, header.Filename)
		if err := saveUploadedFile(inputPath, file); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to save file")
			return
		}

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.InsertOne(ctx, item)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to create catalog entry")
			return
		}

		q.Add(queue.Job{
			UUID:        contentUUID,
			InputPath:   inputPath,
			OutputDir:   layout.Dir,
			OutputName:  layout.OutputName,
			ContentType: contentType,
			Season:      season,
			Episode:     episode,
		})
		q.Start()

		utils.Success(w, http.StatusCreated, map[string]any{
			"status": "success",
			"id":     result.InsertedID,
		})
	}
}

func handleAddEpisode(client *mongo.Client, cfg *config.Config, q *queue.ConversionQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentUUID := chi.URLParam(r, "id")

		if err := r.ParseMultipartForm(cfg.MaxFileSize); err != nil {
			utils.Error(w, http.StatusBadRequest, "File too large or invalid form data")
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "File is required")
			return
		}
		defer file.Close()

		if !videoExtAllowed(header.Filename) {
			utils.Error(w, http.StatusBadRequest, fmt.Sprintf("File type %s not allowed", filepath.Ext(header.Filename)))
			return
		}

		metadata := parseEpisodeMetadata(r)

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var item models.CatalogItem
		if err := coll.FindOne(ctx, bson.M{"uuid": contentUUID}).Decode(&item); err != nil {
			utils.Error(w, http.StatusNotFound, "Content not found")
			return
		}

		if item.Type != "tvshow" {
			utils.Error(w, http.StatusBadRequest, "Content is not a TV show")
			return
		}

		layout := storage.EpisodeLayout(item.Title, metadata.Season, metadata.Episode)
		fullPath := storage.Resolve(cfg.UploadFolder, layout.Dir)

		if err := os.MkdirAll(fullPath, 0755); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to create directory")
			return
		}

		inputPath := filepath.Join(fullPath, header.Filename)
		if err := saveUploadedFile(inputPath, file); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to save file")
			return
		}

		newEpisode := models.Episode{
			EpisodeNumber: metadata.Episode,
			Title:         metadata.Title,
			Status:        "In-Progress",
		}

		seasonExists := false
		for _, s := range item.Seasons {
			if s.SeasonNumber == metadata.Season {
				seasonExists = true
				break
			}
		}

		if seasonExists {
			coll.UpdateOne(ctx,
				bson.M{"uuid": contentUUID},
				bson.M{
					"$push": bson.M{"seasons.$[s].episodes": newEpisode},
					"$set":  bson.M{"updated_at": time.Now()},
				},
				options.UpdateOne().SetArrayFilters([]interface{}{
					bson.M{"s.season_number": metadata.Season},
				}),
			)
		} else {
			coll.UpdateOne(ctx,
				bson.M{"uuid": contentUUID},
				bson.M{
					"$push": bson.M{"seasons": models.Season{
						SeasonNumber: metadata.Season,
						Episodes:     []models.Episode{newEpisode},
					}},
					"$set": bson.M{"updated_at": time.Now()},
				},
			)
		}

		q.Add(queue.Job{
			UUID:        contentUUID,
			InputPath:   inputPath,
			OutputDir:   layout.Dir,
			OutputName:  layout.OutputName,
			ContentType: "tvshow",
			Season:      metadata.Season,
			Episode:     metadata.Episode,
		})
		q.Start()

		utils.Success(w, http.StatusOK, map[string]any{
			"status":  "success",
			"message": "Episode queued for processing",
		})
	}
}

func handleQueueInfo(q *queue.ConversionQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info := q.Info()
		// Sanitize: strip absolute paths and truncate error messages to prevent
		// leaking host filesystem layout or raw ffmpeg output to authenticated users.
		if jobs, ok := info["jobs"].([]any); ok {
			for _, j := range jobs {
				if job, ok := j.(map[string]any); ok {
					if v, ok := job["input_path"].(string); ok {
						job["input_path"] = filepath.Base(v)
					}
					if v, ok := job["output_dir"].(string); ok {
						job["output_dir"] = filepath.Base(v)
					}
					if v, ok := job["error"].(string); ok && len(v) > 200 {
						job["error"] = v[:200] + "...(truncated)"
					}
				}
			}
		}
		utils.Success(w, http.StatusOK, info)
	}
}

func handleQueueStart(q *queue.ConversionQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q.Start()
		utils.Success(w, http.StatusOK, map[string]any{
			"status":  "success",
			"message": "Queue processor started",
		})
	}
}

func handleQueueCleanup(q *queue.ConversionQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		removed := q.Cleanup()
		utils.Success(w, http.StatusOK, map[string]any{
			"status":  "success",
			"removed": removed,
		})
	}
}
